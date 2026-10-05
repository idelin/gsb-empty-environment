import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitWriter } from '../src/bitstream.ts';
import { FormatError } from '../src/errors.ts';
import {
  CanonicalDecoder,
  MAX_CODE_LENGTH,
  buildCodeLengths,
  canonicalCodes,
  isKraftValid,
  validateCodeLengths,
} from '../src/huffman.ts';
import { rng } from './helpers.ts';

test('单符号码长为 1；双符号各为 1', () => {
  const one = buildCodeLengths([5]);
  assert.deepEqual([...one], [1]);
  const two = buildCodeLengths([3, 7]);
  assert.deepEqual([...two], [1, 1]);
});

test('规范码字按 (码长, 符号) 分配', () => {
  // 码长: 符号0→1, 符号1→2, 符号2→3, 符号3→3（Kraft = 1）
  const lengths = [1, 2, 3, 3];
  const codes = canonicalCodes(lengths);
  assert.equal(codes[0], 0b0);
  assert.equal(codes[1], 0b10);
  assert.equal(codes[2], 0b110);
  assert.equal(codes[3], 0b111);
});

test('过度订阅被拒绝，欠订阅被允许', () => {
  assert.throws(() => validateCodeLengths([1, 1, 1]), FormatError);
  assert.throws(() => validateCodeLengths([1, 1, 1]), /过度订阅/);
  validateCodeLengths([2, 2, 2]); // Kraft = 3/4，欠订阅合法
  validateCodeLengths([1]); // 单符号欠订阅合法
  assert.throws(() => validateCodeLengths([0, 0, 0]), /为空/);
  assert.throws(() => validateCodeLengths([16]), FormatError);
});

test('极端偏斜频率（斐波那契）限长且 Kraft 合法', () => {
  const fib = [1, 1];
  while (fib.length < 40) fib.push(fib[fib.length - 1] + fib[fib.length - 2]);
  const lengths = buildCodeLengths(fib);
  let maxLen = 0;
  for (const l of lengths) maxLen = Math.max(maxLen, l);
  assert.ok(maxLen <= MAX_CODE_LENGTH, `最大码长 ${maxLen} 超限`);
  assert.ok(isKraftValid(lengths));
  // 完整树：Kraft 和应恰好为 1
  let kraft = 0;
  for (const l of lengths) if (l > 0) kraft += 2 ** (MAX_CODE_LENGTH - l);
  assert.equal(kraft, 2 ** MAX_CODE_LENGTH);
});

test('随机频率表: 码长合法且位级往返一致', () => {
  const rand = rng(88);
  for (let iter = 0; iter < 300; iter++) {
    const n = 1 + Math.floor(rand() * 60);
    const freqs = new Array<number>(286).fill(0);
    const symbols: number[] = [];
    for (let k = 0; k < n; k++) {
      const s = Math.floor(rand() * 286);
      // 高度偏斜的频率分布
      freqs[s] = 1 + Math.floor(Math.pow(rand(), 8) * 100000);
    }
    for (let s = 0; s < 286; s++) if (freqs[s] > 0) symbols.push(s);
    const lengths = buildCodeLengths(freqs);
    assert.ok(isKraftValid(lengths), 'Kraft 校验失败');
    const codes = canonicalCodes(lengths);
    const decoder = new CanonicalDecoder(lengths);
    // 写一段随机符号序列，逐位读回应完全一致
    const writer = new BitWriter();
    const seq: number[] = [];
    for (let k = 0; k < 200; k++) {
      const s = symbols[Math.floor(rand() * symbols.length)];
      seq.push(s);
      writer.writeCode(codes[s], lengths[s]);
    }
    const bytes = writer.finish();
    let bitPos = 0;
    const readBit = (): number | null => {
      if (bitPos >= bytes.length * 8) return null;
      const bit = (bytes[bitPos >> 3] >> (bitPos & 7)) & 1;
      bitPos++;
      return bit;
    };
    for (const want of seq) {
      const got = decoder.decode(readBit);
      assert.equal(got, want);
    }
  }
});

test('非法位型被检出（欠订阅码的未覆盖分支）', () => {
  // 单符号码：唯一合法码字是 0；以 1 开头的位型必须在 15 位内报错
  const decoder = new CanonicalDecoder([1]);
  const bits = [1, ...new Array<number>(15).fill(0)];
  let pos = 0;
  assert.throws(
    () => decoder.decode(() => (pos < bits.length ? bits[pos++] : null)),
    FormatError,
  );
});
