/**
 * 运行样例：对结构化指令序列执行 CABAC 编码，并与无上下文静态模型对比。
 * 运行：node src/demo.ts   （Node >= 22.6，原生 TypeScript 类型擦除）
 */
import { CabacEncoder } from './cabac.ts';
import { BitWriter } from './bitstream.ts';
import {
  OPCODES,
  createInstructionContexts,
  encodeInstruction,
  encodeSequence,
  decodeSequence,
  generateProgram,
  generateLoopStream,
  type Instruction,
} from './isa.ts';
import {
  trainStaticContexts,
  encodeSequenceStatic,
  decodeSequenceStatic,
} from './static_model.ts';

const N = 100_000;
const fmt = (x: number, d = 3) => x.toFixed(d);

function convergenceCurve(
  instrs: Instruction[],
  checkpoints: number[],
): Array<[number, number]> {
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, true);
  const ctxs = createInstructionContexts();
  const curve: Array<[number, number]> = [];
  let prev: Instruction | null = null;
  for (let i = 0; i < instrs.length; i++) {
    encodeInstruction(enc, ctxs, instrs[i], prev);
    prev = instrs[i];
    if (checkpoints.includes(i + 1)) curve.push([i + 1, writer.bitsWritten / (i + 1)]);
  }
  enc.finish();
  return curve;
}

function report(name: string, instrs: Instruction[]): void {
  const n = instrs.length;
  const cabac = encodeSequence(instrs, true);
  const uniform = encodeSequence(instrs, false);            // 静态均匀 p=0.5（无上下文）
  const staticCtxs = trainStaticContexts(instrs);           // 静态训练（全局直方图，无上下文）
  const trained = encodeSequenceStatic(instrs, staticCtxs);
  const rawBits = n * 11; // 定长：op 3bit + rd 4bit + rs 4bit

  console.log(`【${name}】 ${n} 条指令`);
  console.log('  模型                        总比特     比特/指令    相对 CABAC');
  console.log(`  原始定长（不压缩）       ${String(rawBits).padStart(9)}     ${fmt(rawBits / n, 2)} bit    ${fmt(rawBits / cabac.bits, 1)}x`);
  console.log(`  静态均匀（p=0.5 冻结）   ${String(uniform.bits).padStart(9)}     ${fmt(uniform.bits / n, 2)} bit    ${fmt(uniform.bits / cabac.bits, 1)}x`);
  console.log(`  静态训练（全局直方图）   ${String(trained.bits).padStart(9)}     ${fmt(trained.bits / n, 2)} bit    ${fmt(trained.bits / cabac.bits, 2)}x`);
  console.log(`  CABAC（上下文自适应）    ${String(cabac.bits).padStart(9)}     ${fmt(cabac.bits / n, 3)} bit    1.0x`);

  // 往返校验
  const decoded = decodeSequence(cabac.data, n, true);
  const ok = decoded.every((d, i) => d.op === instrs[i].op && d.rd === instrs[i].rd && d.rs === instrs[i].rs);
  const decodedS = decodeSequenceStatic(trained.data, n, staticCtxs.map(c => ({ ...c })));
  const okS = decodedS.every((d, i) => d.op === instrs[i].op && d.rd === instrs[i].rd && d.rs === instrs[i].rs);
  console.log(`  往返校验：CABAC ${ok ? 'OK' : 'FAIL'}，静态训练模型 ${okS ? 'OK' : 'FAIL'}\n`);
}

// ---------- 实验 1：高频指令主导流 —— 有效位长收敛至 ~0.2 比特 ----------
const dominant: Instruction = { op: 0, rd: 0, rs: 0 }; // MOV r0, r0
const loopStream = generateLoopStream(N, 7, dominant, 0.006);
const counts = new Array(OPCODES.length).fill(0);
for (const instr of loopStream) counts[instr.op]++;
console.log(`高频指令：${OPCODES[dominant.op]} ${JSON.stringify(dominant)}，占比 ${(counts[dominant.op] / N * 100).toFixed(2)}%\n`);
console.log('CABAC 有效编码位长收敛曲线（比特/指令）：');
for (const [n, b] of convergenceCurve(loopStream, [100, 1_000, 10_000, 50_000, 100_000])) {
  console.log(`  前 ${String(n).padStart(6)} 条：${fmt(b)} bit/instr`);
}
console.log('');
report('高频指令流（延时循环形态）', loopStream);

// ---------- 实验 2：多相位结构化程序 —— 非平稳信源 ----------
const program = generateProgram(N, 42);
console.log('多相位结构化程序收敛曲线（比特/指令）：');
for (const [n, b] of convergenceCurve(program, [100, 1_000, 10_000, 50_000, 100_000])) {
  console.log(`  前 ${String(n).padStart(6)} 条：${fmt(b)} bit/instr`);
}
console.log('');
report('多相位结构化程序（紧循环 + 顺序块）', program);
