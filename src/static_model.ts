/**
 * 对照基线：无上下文的静态算术编码模型。
 *
 * 与 CABAC 使用完全相同的二值化方案，但：
 *   - 上下文按 bin 位置池化（repeat / 一元位置 / 字段比特位），
 *     不做前一指令条件化、不做前缀树条件化；
 *   - 概率取自全局直方图，编码前一次性训练后冻结（adaptive = false）。
 */
import { CabacEncoder, CabacDecoder, createContexts, type ContextModel } from './cabac.ts';
import { stateForProbability } from './tables.ts';
import { BitWriter, BitReader } from './bitstream.ts';
import { NUM_OPCODES, type Instruction } from './isa.ts';

const REPEAT_CTX = 0;
const UNARY_BASE = 1;                    // 7 个一元位置
const RD_BASE = UNARY_BASE + NUM_OPCODES - 1; // 4 个比特位
const RS_BASE = RD_BASE + 4;
export const NUM_STATIC_CTX = RS_BASE + 4;

function sameInstruction(a: Instruction | null, b: Instruction): boolean {
  return a !== null && a.op === b.op && a.rd === b.rd && a.rs === b.rs;
}

/** 把一条指令二值化为 (上下文编号, bin) 序列 */
function binarize(instr: Instruction, prev: Instruction | null, out: Array<[number, 0 | 1]>): void {
  if (sameInstruction(prev, instr)) {
    out.push([REPEAT_CTX, 1]);
    return;
  }
  out.push([REPEAT_CTX, 0]);
  for (let i = 0; i < instr.op; i++) out.push([UNARY_BASE + i, 1]);
  if (instr.op < NUM_OPCODES - 1) out.push([UNARY_BASE + instr.op, 0]);
  for (let i = 0; i < 4; i++) out.push([RD_BASE + i, ((instr.rd >> (3 - i)) & 1) as 0 | 1]);
  for (let i = 0; i < 4; i++) out.push([RS_BASE + i, ((instr.rs >> (3 - i)) & 1) as 0 | 1]);
}

/** 第一遍：统计全局直方图，训练出每个位置的冻结概率状态 */
export function trainStaticContexts(instrs: Instruction[]): ContextModel[] {
  const ones = new Array<number>(NUM_STATIC_CTX).fill(0);
  const total = new Array<number>(NUM_STATIC_CTX).fill(0);
  const bins: Array<[number, 0 | 1]> = [];
  let prev: Instruction | null = null;
  for (const instr of instrs) {
    bins.length = 0;
    binarize(instr, prev, bins);
    for (const [ctxIdx, bit] of bins) {
      total[ctxIdx]++;
      ones[ctxIdx] += bit;
    }
    prev = instr;
  }
  return createContexts(NUM_STATIC_CTX).map((ctx, i) => {
    if (total[i] === 0) return ctx;
    const { state, mps } = stateForProbability(ones[i] / total[i]);
    return { state, mps };
  });
}

export function encodeSequenceStatic(instrs: Instruction[], ctxs: ContextModel[]): { data: Uint8Array; bits: number } {
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, false); // 冻结：纯静态
  const bins: Array<[number, 0 | 1]> = [];
  let prev: Instruction | null = null;
  for (const instr of instrs) {
    bins.length = 0;
    binarize(instr, prev, bins);
    for (const [ctxIdx, bit] of bins) enc.encodeBin(bit, ctxs[ctxIdx]);
    prev = instr;
  }
  const data = enc.finish();
  return { data, bits: writer.bitsWritten };
}

export function decodeSequenceStatic(data: Uint8Array, count: number, ctxs: ContextModel[]): Instruction[] {
  const dec = new CabacDecoder(new BitReader(data), false);
  const out: Instruction[] = [];
  let prev: Instruction | null = null;
  for (let n = 0; n < count; n++) {
    if (dec.decodeBin(ctxs[REPEAT_CTX]) === 1) {
      if (prev === null) throw new Error('bitstream error: repeat flag without previous instruction');
      out.push({ ...prev });
      continue;
    }
    let op = 0;
    while (op < NUM_OPCODES - 1 && dec.decodeBin(ctxs[UNARY_BASE + op]) === 1) op++;
    let rd = 0;
    for (let i = 0; i < 4; i++) rd = (rd << 1) | dec.decodeBin(ctxs[RD_BASE + i]);
    let rs = 0;
    for (let i = 0; i < 4; i++) rs = (rs << 1) | dec.decodeBin(ctxs[RS_BASE + i]);
    prev = { op, rd, rs };
    out.push(prev);
  }
  return out;
}
