import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OPCODES,
  encodeSequence,
  decodeSequence,
  generateProgram,
  generateLoopStream,
  mulberry32,
  type Instruction,
} from '../src/isa.ts';
import {
  trainStaticContexts,
  encodeSequenceStatic,
  decodeSequenceStatic,
} from '../src/static_model.ts';
import { CabacEncoder } from '../src/cabac.ts';
import { BitWriter } from '../src/bitstream.ts';
import { createInstructionContexts, encodeInstruction } from '../src/isa.ts';

const equalInstrs = (a: Instruction[], b: Instruction[]) =>
  a.length === b.length && a.every((x, i) => x.op === b[i].op && x.rd === b[i].rd && x.rs === b[i].rs);

test('指令序列往返：结构化程序（CABAC 自适应）', () => {
  const program = generateProgram(20_000, 42);
  const { data } = encodeSequence(program, true);
  const decoded = decodeSequence(data, program.length, true);
  assert.ok(equalInstrs(decoded, program));
});

test('指令序列往返：随机指令 + 边界（空序列 / 单条 / 全部操作码）', () => {
  const rand = mulberry32(99);
  const random: Instruction[] = Array.from({ length: 5_000 }, () => ({
    op: Math.floor(rand() * OPCODES.length),
    rd: Math.floor(rand() * 16),
    rs: Math.floor(rand() * 16),
  }));
  assert.ok(equalInstrs(decodeSequence(encodeSequence(random).data, random.length), random));
  assert.deepEqual(decodeSequence(encodeSequence([]).data, 0), []);
  const single = [{ op: 7, rd: 15, rs: 15 }];
  assert.ok(equalInstrs(decodeSequence(encodeSequence(single).data, 1), single));
});

test('静态训练模型：自身往返正确', () => {
  const program = generateProgram(10_000, 7);
  const ctxs = trainStaticContexts(program);
  const { data } = encodeSequenceStatic(program, ctxs);
  const decoded = decodeSequenceStatic(data, program.length, ctxs.map(c => ({ ...c })));
  assert.ok(equalInstrs(decoded, program));
});

test('验收：高频指令有效编码位长收敛至约 0.2 比特', () => {
  // 高频指令 MOV r0,r0 占比 ~99.4% 的延时循环形态指令流
  const stream = generateLoopStream(100_000, 7, { op: 0, rd: 0, rs: 0 }, 0.006);
  const writer = new BitWriter();
  const enc = new CabacEncoder(writer, true);
  const ctxs = createInstructionContexts();
  const marks: Array<[number, number]> = [];
  let prev: Instruction | null = null;
  for (let i = 0; i < stream.length; i++) {
    encodeInstruction(enc, ctxs, stream[i], prev);
    prev = stream[i];
    if ([1_000, 10_000, 50_000, 100_000].includes(i + 1)) marks.push([i + 1, writer.bitsWritten / (i + 1)]);
  }
  enc.finish();
  // 随编码推进单调收敛
  for (let i = 1; i < marks.length; i++) {
    assert.ok(marks[i][1] < marks[i - 1][1], `应单调下降：${JSON.stringify(marks)}`);
  }
  const final = marks[marks.length - 1][1];
  // 收敛至 0.2 比特附近
  assert.ok(final < 0.25, `最终 ${final} bit/instr 应收敛至 0.2 附近`);
  assert.ok(final > 0.1, `最终 ${final} bit/instr 不应异常偏低`);
});

test('验收：整体压缩率大幅领先无上下文静态模型', () => {
  // 高频指令流：CABAC vs 静态均匀（无上下文）应大幅领先
  const stream = generateLoopStream(100_000, 7, { op: 0, rd: 0, rs: 0 }, 0.006);
  const cabac = encodeSequence(stream, true);
  const uniform = encodeSequence(stream, false);
  const rawBits = stream.length * 11;
  assert.ok(cabac.bits * 4 <= uniform.bits,
    `CABAC(${cabac.bits}) 应至少 4 倍优于静态均匀模型(${uniform.bits})`);
  assert.ok(cabac.bits * 20 <= rawBits,
    `CABAC(${cabac.bits}) 应至少 20 倍优于原始定长(${rawBits})`);

  // 非平稳多相位流：CABAC 应显著优于训练后冻结的静态模型（全局直方图）
  const program = generateProgram(100_000, 42);
  const cabac2 = encodeSequence(program, true);
  const trained = encodeSequenceStatic(program, trainStaticContexts(program));
  assert.ok(cabac2.bits * 1.3 <= trained.bits,
    `CABAC(${cabac2.bits}) 应至少 1.3 倍优于静态训练模型(${trained.bits})`);
});
