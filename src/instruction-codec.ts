import { ContextModel, ContextStore, type Bin } from "./context.ts";
import { CabacDecoder } from "./decoder.ts";
import { CabacEncoder } from "./encoder.ts";
import { stateIdxForLpsProbability } from "./tables.ts";
import { NUM_OPS, OP_MOV, hasImmediate, type Instruction } from "./instruction-stream.ts";

/**
 * Instruction-stream codec built on the CABAC kernel.
 *
 * Binarization (prefix-free opcode tree, MOV is the hot 1-bin path):
 *   MOV:   1
 *   ADD:   0 1 1        SUB:   0 1 0
 *   LOAD:  0 0 1        STORE: 0 0 0 1      JMP: 0 0 0 0
 * followed by 4 context-coded bins for the immediate (if any).
 *
 * Context layout — every syntax element gets independent adaptive contexts,
 * and the opcode bins are additionally conditioned on the previous opcode
 * (first-order structure):
 *   [0..5]    bin0 ("op == MOV")     by prevOp
 *   [6..11]   bin1 ("op in ADD/SUB") by prevOp
 *   [12..13]  bin2 (subtree split)
 *   [14]      bin3 (STORE vs JMP)
 *   [16..39]  immediate bins, per (op, bit position)
 */
const CTX_BIN0 = 0;
const CTX_BIN1 = 6;
const CTX_BIN2 = 12;
const CTX_BIN3 = 14;
const CTX_IMM = 16;
export const CTX_COUNT = CTX_IMM + NUM_OPS * 4;

const IMM_BITS = 4;

export interface EncodedStream {
  /** Full bitstream: 4-byte big-endian instruction count + CABAC payload. */
  bytes: Uint8Array;
  /** CABAC payload size in bytes (excluding the count header). */
  payloadBytes: number;
  /** Emitted-bit cost of each instruction (encoder-side measurement). */
  bitsPerInstruction: number[];
}

function encodeOpcodeBins(enc: CabacEncoder, store: ContextStore, op: number, prevOp: number): void {
  if (op === OP_MOV) {
    enc.encodeBin(store.get(CTX_BIN0 + prevOp), 1);
    return;
  }
  enc.encodeBin(store.get(CTX_BIN0 + prevOp), 0);
  if (op === 1 || op === 2) {
    enc.encodeBin(store.get(CTX_BIN1 + prevOp), 1);
    enc.encodeBin(store.get(CTX_BIN2), op === 1 ? 1 : 0);
  } else {
    enc.encodeBin(store.get(CTX_BIN1 + prevOp), 0);
    enc.encodeBin(store.get(CTX_BIN2 + 1), op === 3 ? 1 : 0);
    if (op !== 3) {
      enc.encodeBin(store.get(CTX_BIN3), op === 4 ? 1 : 0);
    }
  }
}

function decodeOpcodeBins(dec: CabacDecoder, store: ContextStore, prevOp: number): number {
  if (dec.decodeBin(store.get(CTX_BIN0 + prevOp)) === 1) return OP_MOV;
  if (dec.decodeBin(store.get(CTX_BIN1 + prevOp)) === 1) {
    return dec.decodeBin(store.get(CTX_BIN2)) === 1 ? 1 : 2;
  }
  if (dec.decodeBin(store.get(CTX_BIN2 + 1)) === 1) return 3;
  return dec.decodeBin(store.get(CTX_BIN3)) === 1 ? 4 : 5;
}

function encodeImm(enc: CabacEncoder, store: ContextStore, op: number, imm: number): void {
  for (let pos = IMM_BITS - 1; pos >= 0; pos--) {
    enc.encodeBin(store.get(CTX_IMM + op * IMM_BITS + (IMM_BITS - 1 - pos)), ((imm >> pos) & 1) as Bin);
  }
}

function decodeImm(dec: CabacDecoder, store: ContextStore, op: number): number {
  let imm = 0;
  for (let pos = 0; pos < IMM_BITS; pos++) {
    imm = (imm << 1) | dec.decodeBin(store.get(CTX_IMM + op * IMM_BITS + pos));
  }
  return imm;
}

/** Encode a stream with the context-adaptive CABAC model. */
export function encodeInstructionStream(stream: Instruction[]): EncodedStream {
  const enc = new CabacEncoder();
  const store = new ContextStore(CTX_COUNT);
  const bitsPerInstruction: number[] = [];
  let prevOp = OP_MOV;
  for (const instr of stream) {
    const before = enc.bitsEmitted;
    encodeOpcodeBins(enc, store, instr.op, prevOp);
    if (hasImmediate(instr.op)) encodeImm(enc, store, instr.op, instr.imm);
    bitsPerInstruction.push(enc.bitsEmitted - before);
    prevOp = instr.op;
  }
  const payload = enc.finish();
  const bytes = new Uint8Array(4 + payload.length);
  new DataView(bytes.buffer).setUint32(0, stream.length);
  bytes.set(payload, 4);
  return { bytes, payloadBytes: payload.length, bitsPerInstruction };
}

/** Decode a bitstream produced by {@link encodeInstructionStream}. */
export function decodeInstructionStream(bytes: Uint8Array): Instruction[] {
  const count = new DataView(bytes.buffer, bytes.byteOffset).getUint32(0);
  const dec = new CabacDecoder(bytes.subarray(4));
  const store = new ContextStore(CTX_COUNT);
  const stream: Instruction[] = [];
  let prevOp = OP_MOV;
  for (let i = 0; i < count; i++) {
    const op = decodeOpcodeBins(dec, store, prevOp);
    const imm = hasImmediate(op) ? decodeImm(dec, store, op) : 0;
    stream.push({ op, imm });
    prevOp = op;
  }
  return stream;
}

// ---------------------------------------------------------------------------
// Static baseline: same binarization and arithmetic coder, but probabilities
// are fixed up front from the global histogram — no contexts, no adaptation.
// ---------------------------------------------------------------------------

const STATIC_CTX_COUNT = 1 + 1 + 2 + 1 + IMM_BITS; // bin0, bin1, bin2 x2, bin3, imm x4
const S_BIN0 = 0;
const S_BIN1 = 1;
const S_BIN2 = 2;
const S_BIN3 = 4;
const S_IMM = 5;

function staticContexts(stream: Instruction[]): ContextStore {
  // Tally every bin of the binarized stream, globally per syntax element.
  const ones = new Array<number>(STATIC_CTX_COUNT).fill(0);
  const total = new Array<number>(STATIC_CTX_COUNT).fill(0);
  const tally = (ctx: number, bin: Bin) => {
    total[ctx]++;
    if (bin === 1) ones[ctx]++;
  };
  for (const instr of stream) {
    const op = instr.op;
    tally(S_BIN0, op === OP_MOV ? 1 : 0);
    if (op !== OP_MOV) {
      const inAddSub = op === 1 || op === 2;
      tally(S_BIN1, inAddSub ? 1 : 0);
      if (inAddSub) {
        tally(S_BIN2, op === 1 ? 1 : 0);
      } else {
        tally(S_BIN2 + 1, op === 3 ? 1 : 0);
        if (op !== 3) tally(S_BIN3, op === 4 ? 1 : 0);
      }
    }
    if (hasImmediate(op)) {
      for (let pos = IMM_BITS - 1; pos >= 0; pos--) {
        tally(S_IMM + (IMM_BITS - 1 - pos), ((instr.imm >> pos) & 1) as Bin);
      }
    }
  }
  const store = new ContextStore(0);
  store.contexts.length = 0;
  for (let c = 0; c < STATIC_CTX_COUNT; c++) {
    // Laplace smoothing; freeze at the state closest to the global rate.
    const pOne = (ones[c] + 1) / (total[c] + 2);
    const valMPS: Bin = pOne >= 0.5 ? 1 : 0;
    const pLps = pOne >= 0.5 ? 1 - pOne : pOne;
    store.contexts.push(new ContextModel(stateIdxForLpsProbability(pLps), valMPS, true));
  }
  return store;
}

function encodeOpcodeBinsStatic(enc: CabacEncoder, store: ContextStore, op: number): void {
  if (op === OP_MOV) {
    enc.encodeBin(store.get(S_BIN0), 1);
    return;
  }
  enc.encodeBin(store.get(S_BIN0), 0);
  if (op === 1 || op === 2) {
    enc.encodeBin(store.get(S_BIN1), 1);
    enc.encodeBin(store.get(S_BIN2), op === 1 ? 1 : 0);
  } else {
    enc.encodeBin(store.get(S_BIN1), 0);
    enc.encodeBin(store.get(S_BIN2 + 1), op === 3 ? 1 : 0);
    if (op !== 3) enc.encodeBin(store.get(S_BIN3), op === 4 ? 1 : 0);
  }
}

/** Encode with the static (context-free, non-adaptive) baseline model. */
export function encodeInstructionStreamStatic(stream: Instruction[]): EncodedStream {
  const store = staticContexts(stream);
  const enc = new CabacEncoder();
  const bitsPerInstruction: number[] = [];
  for (const instr of stream) {
    const before = enc.bitsEmitted;
    encodeOpcodeBinsStatic(enc, store, instr.op);
    if (hasImmediate(instr.op)) {
      for (let pos = IMM_BITS - 1; pos >= 0; pos--) {
        enc.encodeBin(store.get(S_IMM + (IMM_BITS - 1 - pos)), ((instr.imm >> pos) & 1) as Bin);
      }
    }
    bitsPerInstruction.push(enc.bitsEmitted - before);
  }
  const payload = enc.finish();
  const bytes = new Uint8Array(4 + payload.length);
  new DataView(bytes.buffer).setUint32(0, stream.length);
  bytes.set(payload, 4);
  return { bytes, payloadBytes: payload.length, bitsPerInstruction };
}

/** Uncompressed baseline: 3-bit opcode + 4-bit immediate per instruction. */
export function rawBitsPerInstruction(instr: Instruction): number {
  return 3 + (hasImmediate(instr.op) ? IMM_BITS : 0);
}
