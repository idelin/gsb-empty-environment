import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint,
  decodeVarint,
  zigzagEncode,
  zigzagDecode,
  varintLength,
  int64ToUint64,
  uint64ToInt64,
} from '../src/varint.ts';
import { MalformedVarintError, TruncatedFieldError } from '../src/errors.ts';

test('varint 边界值往返: 0/1/127/128/2^32-1/2^63/2^64-1', () => {
  const cases = [0n, 1n, 127n, 128n, 300n, (1n << 32n) - 1n, 1n << 32n,
    (1n << 63n) - 1n, 1n << 63n, (1n << 64n) - 1n];
  for (const v of cases) {
    const enc = encodeVarint(v);
    const dec = decodeVarint(enc, 0);
    assert.equal(dec.value, v, `roundtrip ${v}`);
    assert.equal(dec.bytesRead, enc.length);
    assert.equal(varintLength(v), enc.length);
  }
});

test('varint 7 位压缩编码字节序列与规范一致', () => {
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(127n)], [0x7f]);
  assert.deepEqual([...encodeVarint(128n)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
  assert.deepEqual([...encodeVarint((1n << 64n) - 1n)],
    [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]);
});

test('varint 解码带偏移量', () => {
  const buf = Uint8Array.from([0xaa, 0xac, 0x02, 0xbb]);
  const dec = decodeVarint(buf, 1);
  assert.equal(dec.value, 300n);
  assert.equal(dec.bytesRead, 2);
});

test('varint 截断: 缺少终止字节触发安全越界防护', () => {
  const buf = Uint8Array.from([0x80]); // 只有延续位, 没有后续
  assert.throws(() => decodeVarint(buf, 0), TruncatedFieldError);
});

test('varint 畸形: 超过 10 字节未终止抛 MalformedVarintError', () => {
  const buf = new Uint8Array(11).fill(0x80);
  assert.throws(() => decodeVarint(buf, 0), MalformedVarintError);
});

test('zigzag 规范映射: 0->0, -1->1, 1->2, -2->3, 2->4', () => {
  assert.equal(zigzagEncode(0n), 0n);
  assert.equal(zigzagEncode(-1n), 1n);
  assert.equal(zigzagEncode(1n), 2n);
  assert.equal(zigzagEncode(-2n), 3n);
  assert.equal(zigzagEncode(2n), 4n);
});

test('zigzag int64 极值往返', () => {
  const cases = [0n, 1n, -1n, (1n << 63n) - 1n, -(1n << 63n), 42n, -42n];
  for (const v of cases) {
    assert.equal(zigzagDecode(zigzagEncode(v)), v, `zigzag ${v}`);
  }
});

test('int64/uint64 补码互转', () => {
  assert.equal(int64ToUint64(-1n), (1n << 64n) - 1n);
  assert.equal(uint64ToInt64((1n << 64n) - 1n), -1n);
  assert.equal(uint64ToInt64(int64ToUint64(-(1n << 63n))), -(1n << 63n));
});
