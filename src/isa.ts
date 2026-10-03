/**
 * 指令流语法层：把结构化指令序列二值化为 bin 流，并为每个语法元素
 * 分配独立的 CABAC 上下文集合。
 *
 * 指令格式（类 RISC 微指令）：{ op: 3bit 操作码, rd: 4bit 目的寄存器, rs: 4bit 源寄存器 }
 *
 * 二值化方案：
 *   1. repeat 标志：本条指令与上一条完全相同（利用指令流的时间局部性，
 *      连发相同指令时每条仅需 1 个 bin）；
 *   2. op：截断一元码（截断于 7），上下文按 (上一条 op, 一元位置) 索引 —— 一阶上下文；
 *   3. rd / rs：4bit 定长，MSB 优先，按比特前缀树索引上下文（15 个/字段）。
 */
import { CabacEncoder, CabacDecoder, createContexts, type ContextModel } from './cabac.ts';
import { BitWriter, BitReader } from './bitstream.ts';

export const OPCODES = ['MOV', 'ADD', 'SUB', 'LOAD', 'STORE', 'JMP', 'CALL', 'NOP'] as const;
export const NUM_OPCODES = OPCODES.length;

export interface Instruction {
  op: number;
  rd: number;
  rs: number;
}

export interface InstructionContexts {
  repeat: ContextModel;
  opUnary: ContextModel[]; // NUM_OPCODES(上一条) × (NUM_OPCODES-1)(一元位置)
  rd: ContextModel[];      // 4bit 前缀树，15 个
  rs: ContextModel[];
}

export function createInstructionContexts(): InstructionContexts {
  return {
    repeat: createContexts(1)[0],
    opUnary: createContexts(NUM_OPCODES * (NUM_OPCODES - 1)),
    rd: createContexts(15),
    rs: createContexts(15),
  };
}

/** 4bit 字段，MSB 优先；上下文按已解码前缀索引（满二叉树 1+2+4+8=15） */
function encodeField(enc: CabacEncoder, ctxs: ContextModel[], value: number): void {
  let prefix = 0;
  for (let i = 0; i < 4; i++) {
    const bit = ((value >> (3 - i)) & 1) as 0 | 1;
    enc.encodeBin(bit, ctxs[(1 << i) - 1 + prefix]);
    prefix = (prefix << 1) | bit;
  }
}

function decodeField(dec: CabacDecoder, ctxs: ContextModel[]): number {
  let prefix = 0;
  for (let i = 0; i < 4; i++) {
    const bit = dec.decodeBin(ctxs[(1 << i) - 1 + prefix]);
    prefix = (prefix << 1) | bit;
  }
  return prefix;
}

function sameInstruction(a: Instruction | null, b: Instruction): boolean {
  return a !== null && a.op === b.op && a.rd === b.rd && a.rs === b.rs;
}

export function encodeInstruction(
  enc: CabacEncoder,
  ctxs: InstructionContexts,
  instr: Instruction,
  prev: Instruction | null,
): void {
  if (sameInstruction(prev, instr)) {
    enc.encodeBin(1, ctxs.repeat);
    return;
  }
  enc.encodeBin(0, ctxs.repeat);
  const prevOp = prev ? prev.op : 0;
  const base = prevOp * (NUM_OPCODES - 1);
  // 截断一元码：op 个 1，op < 7 时补一个 0
  for (let i = 0; i < instr.op; i++) enc.encodeBin(1, ctxs.opUnary[base + i]);
  if (instr.op < NUM_OPCODES - 1) enc.encodeBin(0, ctxs.opUnary[base + instr.op]);
  encodeField(enc, ctxs.rd, instr.rd);
  encodeField(enc, ctxs.rs, instr.rs);
}

export function decodeInstruction(
  dec: CabacDecoder,
  ctxs: InstructionContexts,
  prev: Instruction | null,
): Instruction {
  if (dec.decodeBin(ctxs.repeat) === 1) {
    if (prev === null) throw new Error('bitstream error: repeat flag without previous instruction');
    return { ...prev };
  }
  const prevOp = prev ? prev.op : 0;
  const base = prevOp * (NUM_OPCODES - 1);
  let op = 0;
  while (op < NUM_OPCODES - 1 && dec.decodeBin(ctxs.opUnary[base + op]) === 1) op++;
  const rd = decodeField(dec, ctxs.rd);
  const rs = decodeField(dec, ctxs.rs);
  return { op, rd, rs };
}

export function encodeSequence(instrs: Instruction[], adaptive = true): { data: Uint8Array; bits: number } {
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, adaptive);
  const ctxs = createInstructionContexts();
  let prev: Instruction | null = null;
  for (const instr of instrs) {
    encodeInstruction(enc, ctxs, instr, prev);
    prev = instr;
  }
  const data = enc.finish();
  return { data, bits: writer.bitsWritten };
}

export function decodeSequence(data: Uint8Array, count: number, adaptive = true): Instruction[] {
  const dec = new CabacDecoder(new BitReader(data), adaptive);
  const ctxs = createInstructionContexts();
  const out: Instruction[] = [];
  let prev: Instruction | null = null;
  for (let i = 0; i < count; i++) {
    const instr = decodeInstruction(dec, ctxs, prev);
    out.push(instr);
    prev = instr;
  }
  return out;
}

/** 可复现的伪随机数发生器（mulberry32） */
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

/**
 * 生成"结构化指令序列"：模拟真实程序的多相位形态 ——
 *   - 紧循环段：同一条指令（循环体）连续重复，段长几何分布；
 *   - 顺序块：操作码偏斜分布、寄存器与历史强相关的指令串（基本块形态）。
 * 各相位统计特性差异显著（非平稳信源）：重复密度、操作码分布、寄存器
 * 局部性随相位变化，全局静态模型无法同时拟合，只有随历史命中动态调整的
 * 上下文自适应模型能持续跟踪局部统计。
 */
export function generateProgram(count: number, seed = 42): Instruction[] {
  const rand = mulberry32(seed);
  const out: Instruction[] = [];
  const opWeights = [0.3, 0.18, 0.1, 0.16, 0.12, 0.06, 0.03, 0.05]; // MOV 最热
  const pickWeightedOp = (): number => {
    let x = rand();
    for (let i = 0; i < opWeights.length; i++) {
      x -= opWeights[i];
      if (x <= 0) return i;
    }
    return 0;
  };

  let prev: Instruction | null = null;
  while (out.length < count) {
    const phaseLen = 2_000 + Math.floor(rand() * 6_000);
    const phaseEnd = Math.min(out.length + phaseLen, count);
    // 每个相位使用不同的寄存器组（非平稳性：字段分布随相位迁移）
    const regBase = Math.floor(rand() * 4) * 4;
    const pickReg = () => regBase + Math.floor(rand() * 4);
    if (rand() < 0.65) {
      // 紧循环段：循环体指令 + 少量扰动，重复率 ~97%
      const body: Instruction = { op: pickWeightedOp(), rd: pickReg(), rs: pickReg() };
      while (out.length < phaseEnd) {
        out.push(rand() < 0.03
          ? { op: pickWeightedOp(), rd: pickReg(), rs: pickReg() }
          : { ...body });
      }
      prev = out[out.length - 1];
    } else {
      // 顺序块：偏斜操作码 + 寄存器复用 + 短重复串
      while (out.length < phaseEnd) {
        const rd = prev !== null && rand() < 0.75 ? prev.rd : pickReg();
        const rs = prev !== null && rand() < 0.6 ? prev.rs : pickReg();
        prev = { op: pickWeightedOp(), rd, rs };
        out.push(prev);
        // 短重复串（如地址递增的连续 LOAD/STORE）
        if (rand() < 0.25 && out.length < phaseEnd) out.push({ ...prev });
      }
    }
  }
  return out.slice(0, count);
}

/**
 * 生成"高频指令主导"的指令流：一条热指令（默认 MOV r0, r0）以几何分布
 * 段长连续重复（延时循环/块拷贝展开形态），夹杂少量随机指令。
 * 用于展示高频指令的有效编码位长随编码推进收敛到亚比特级别。
 */
export function generateLoopStream(
  count: number,
  seed = 7,
  body: Instruction = { op: 0, rd: 0, rs: 0 },
  noise = 0.01,
): Instruction[] {
  const rand = mulberry32(seed);
  const out: Instruction[] = [];
  while (out.length < count) {
    out.push(rand() < noise
      ? { op: 1 + Math.floor(rand() * (NUM_OPCODES - 1)), rd: Math.floor(rand() * 16), rs: Math.floor(rand() * 16) }
      : { ...body });
  }
  return out;
}
