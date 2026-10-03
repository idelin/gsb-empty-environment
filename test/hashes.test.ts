import { test } from 'node:test';
import assert from 'node:assert/strict';
import { md5_32, murmur3_32, fnv1a_32, truncateBits } from '../src/hashes.ts';

test('MD5-32 已知向量：空串与 "hello"', () => {
  // md5("") = d41d8cd98f00b204... 取大端前 4 字节
  assert.equal(md5_32(''), 0xd41d8cd9);
  // md5("hello") = 5d41402abc4b2a76...
  assert.equal(md5_32('hello'), 0x5d41402a);
});

test('MurmurHash3 x86_32 已知向量', () => {
  assert.equal(murmur3_32(''), 0);
  assert.equal(murmur3_32('hello'), 0x248bfa47);
  assert.equal(murmur3_32('hello', 42), 0xe2dbd2e1);
  assert.equal(murmur3_32('foobar'), 0xa4c4d4bd);
  assert.equal(murmur3_32('foo'), 0xf6a5c420);
});

test('MurmurHash3 x86_32 通过 smhasher VerificationTest（0xB0F57EE3）', () => {
  // smhasher 标准验证：hash(key[0..i), seed=256-i) 级联后再哈希
  const key = new Uint8Array(256);
  for (let i = 0; i < 256; i++) key[i] = i;
  const hashes = new Uint8Array(4 * 256);
  for (let i = 0; i < 256; i++) {
    const h = murmur3_32(key.subarray(0, i), 256 - i);
    hashes[i * 4] = h & 0xff;
    hashes[i * 4 + 1] = (h >>> 8) & 0xff;
    hashes[i * 4 + 2] = (h >>> 16) & 0xff;
    hashes[i * 4 + 3] = (h >>> 24) & 0xff;
  }
  assert.equal(murmur3_32(hashes), 0xb0f57ee3);
});

test('FNV-1a 32 已知向量', () => {
  assert.equal(fnv1a_32(''), 0x811c9dc5);
  assert.equal(fnv1a_32('a'), 0xe40c292c);
  assert.equal(fnv1a_32('foobar'), 0xbf9cf968);
});

test('哈希输出恒为 uint32 且确定', () => {
  for (const fn of [md5_32, murmur3_32, fnv1a_32]) {
    const v = fn('deterministic-key');
    assert.ok(Number.isInteger(v) && v >= 0 && v <= 0xffffffff);
    assert.equal(fn('deterministic-key'), v);
  }
});

test('truncateBits 低位/高位截断正确', () => {
  const constFn = () => 0xdeadbeef;
  assert.equal(truncateBits(constFn, 8, 'low')('x'), 0xef);
  assert.equal(truncateBits(constFn, 8, 'high')('x'), 0xde);
  assert.equal(truncateBits(constFn, 16, 'low')('x'), 0xbeef);
  assert.equal(truncateBits(constFn, 32, 'low')('x'), 0xdeadbeef);
  assert.throws(() => truncateBits(constFn, 0), RangeError);
  assert.throws(() => truncateBits(constFn, 33), RangeError);
});
