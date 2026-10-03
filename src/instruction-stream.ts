/**
 * Structured instruction-stream model used to exercise the CABAC kernel.
 *
 * Opcodes follow a first-order Markov structure (long runs of the hot MOV
 * instruction, basic blocks restarting after JMP, ...), and each opcode has
 * its own skewed 4-bit immediate distribution. This mimics the statistical
 * redundancy found in real syntax-element streams.
 */

export const OPCODES = ["MOV", "ADD", "SUB", "LOAD", "STORE", "JMP"] as const;
export const NUM_OPS = OPCODES.length;
export const OP_MOV = 0;
export const OP_JMP = 5;

export interface Instruction {
  op: number;
  /** 4-bit immediate operand (unused for JMP). */
  imm: number;
}

export function hasImmediate(op: number): boolean {
  return op !== OP_JMP;
}

/** Deterministic PRNG (mulberry32) for reproducible streams. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Probability that the next opcode is MOV, conditioned on the previous one. */
function movTransitionProb(prevOp: number): number {
  if (prevOp === OP_MOV) return 0.96; // hot runs
  if (prevOp === OP_JMP) return 0.9; // basic blocks open with MOV
  return 0.6;
}

const NON_MOV_WEIGHTS = [0, 0.3, 0.2, 0.2, 0.15, 0.15];

function sampleNextOp(prevOp: number, rnd: () => number): number {
  if (rnd() < movTransitionProb(prevOp)) return OP_MOV;
  const total = NON_MOV_WEIGHTS.reduce((a, b) => a + b, 0);
  let r = rnd() * total;
  for (let op = 1; op < NUM_OPS; op++) {
    r -= NON_MOV_WEIGHTS[op];
    if (r <= 0) return op;
  }
  return NUM_OPS - 1;
}

/** Per-opcode skewed immediate distributions (values 0..15). */
function sampleImm(op: number, rnd: () => number): number {
  const r = rnd();
  switch (op) {
    case 0: // MOV: mostly tiny constants
      return r < 0.55 ? 0 : r < 0.8 ? 1 : r < 0.92 ? 2 : 3 + Math.floor(rnd() * 13);
    case 1: // ADD: small increments
      return r < 0.6 ? 1 : r < 0.85 ? 2 : Math.floor(rnd() * 16);
    case 2: // SUB: small decrements
      return r < 0.6 ? 1 : r < 0.85 ? 3 : Math.floor(rnd() * 16);
    case 3: // LOAD: word-aligned offsets
      return (r < 0.7 ? Math.floor(rnd() * 4) : Math.floor(rnd() * 8)) * 2;
    case 4: // STORE: word-aligned offsets
      return (r < 0.7 ? Math.floor(rnd() * 4) : Math.floor(rnd() * 8)) * 2;
    default:
      return 0;
  }
}

/** Generate a structured instruction stream of `count` instructions. */
export function generateInstructionStream(count: number, seed = 1234): Instruction[] {
  const rnd = mulberry32(seed);
  const stream: Instruction[] = [];
  let prevOp = OP_MOV;
  for (let i = 0; i < count; i++) {
    const op = sampleNextOp(prevOp, rnd);
    stream.push({ op, imm: hasImmediate(op) ? sampleImm(op, rnd) : 0 });
    prevOp = op;
  }
  return stream;
}
