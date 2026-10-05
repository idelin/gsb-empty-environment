import { test } from 'node:test';
import { encodeBlock, decodeBlock } from '../src/block.ts';
import { tokenize } from '../src/lz77.ts';
import { expandTokens } from './reference-expander.ts';
import { assertBytesEqual, decodeStreaming, randomBytes, rng } from './helpers.ts';

const WINDOW = 65535;

function* strings(alphabet: number[], maxLen: number): Generator<Uint8Array> {
  const total = Math.pow(alphabet.length, maxLen);
  for (let len = 0; len <= maxLen; len++) {
    const count = Math.pow(alphabet.length, len);
    for (let k = 0; k < count; k++) {
      const s = new Uint8Array(len);
      let v = k;
      for (let i = 0; i < len; i++) {
        s[i] = alphabet[v % alphabet.length];
        v = Math.floor(v / alphabet.length);
      }
      yield s;
    }
  }
  void total;
}

test('穷举小串往返: 二元字母表长度 ≤ 12', () => {
  let count = 0;
  for (const s of strings([0x61, 0x62], 12)) {
    // 参考展开器交叉验证 token 序列
    assertBytesEqual(expandTokens(tokenize(s, WINDOW)), s);
    // 完整块往返
    assertBytesEqual(decodeBlock(encodeBlock(s, WINDOW)), s);
    count++;
  }
  if (count !== 8191) throw new Error(`用例数异常: ${count}`);
});

test('穷举小串往返: 三元字母表长度 ≤ 8', () => {
  let count = 0;
  for (const s of strings([0, 1, 2], 8)) {
    assertBytesEqual(expandTokens(tokenize(s, WINDOW)), s);
    assertBytesEqual(decodeBlock(encodeBlock(s, WINDOW)), s);
    count++;
  }
  if (count !== 9841) throw new Error(`用例数异常: ${count}`);
});

test('随机数据往返: 多种字母表规模与窗口', () => {
  const rand = rng(20261005);
  for (let iter = 0; iter < 200; iter++) {
    const len = Math.floor(rand() * 3000);
    const alphabet = [1, 2, 3, 5, 16, 64, 256][Math.floor(rand() * 7)];
    const window = [1, 3, 17, 256, 4096, 65535][Math.floor(rand() * 6)];
    const data = randomBytes(rand, len, alphabet);
    assertBytesEqual(expandTokens(tokenize(data, window)), data);
    assertBytesEqual(decodeBlock(encodeBlock(data, window)), data);
  }
});

test('流式解码: 任意分块与额度组合', () => {
  const rand = rng(12345);
  for (let iter = 0; iter < 100; iter++) {
    const len = Math.floor(rand() * 2000);
    const alphabet = [1, 2, 4, 32, 256][Math.floor(rand() * 5)];
    const data = randomBytes(rand, len, alphabet);
    const block = encodeBlock(data, WINDOW);
    const mk = () => 1 + Math.floor(rand() * 9);
    const chunkSizes = Array.from({ length: 4 }, mk);
    const quotas = Array.from({ length: 4 }, mk);
    assertBytesEqual(decodeStreaming(block, chunkSizes, quotas), data);
  }
});

test('流式解码: 逐字节输入 + 逐字节输出额度', () => {
  const rand = rng(777);
  const data = randomBytes(rand, 500, 4);
  const block = encodeBlock(data, WINDOW);
  assertBytesEqual(decodeStreaming(block, [1], [1]), data);
});

test('窗口上限为 1 时只能使用距离 1', () => {
  const rand = rng(42);
  const data = randomBytes(rand, 1000, 3);
  const tokens = tokenize(data, 1);
  for (const t of tokens) {
    if (t.type === 'match' && t.distance !== 1) {
      throw new Error(`窗口 1 下出现距离 ${t.distance}`);
    }
  }
  assertBytesEqual(expandTokens(tokens), data);
  assertBytesEqual(decodeBlock(encodeBlock(data, 1)), data);
});
