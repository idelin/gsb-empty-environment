import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint, decodeVarint,
  zigzagEncode32, zigzagDecode32, zigzagEncode64, zigzagDecode64,
  TruncationError, MalformedError,
} from '../src/index.ts';

test('varint: 已知向量', () => {
  assert.deepEqual([...encodeVarint(0)], [0x00]);
  assert.deepEqual([...encodeVarint(1)], [0x01]);
  assert.deepEqual([...encodeVarint(127)], [0x7f]);
  assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
  assert.deepEqual([...encodeVarint(150)], [0x96, 0x01]);
});

test('varint: 边界往返（含 2^64-1 与负数补码）', () => {
  const cases = [0n, 1n, 127n, 128n, 300n, (1n << 32n) - 1n, 1n << 32n, (1n << 53n) - 1n, (1n << 63n), (1n << 64n) - 1n];
  for (const v of cases) {
    const { value, bytesRead } = decodeVarint(encodeVarint(v));
    assert.equal(value, v);
    assert.equal(bytesRead, encodeVarint(v).length);
  }
  // 负数按 64 位补码编码为 10 字节
  const neg = encodeVarint(-1n);
  assert.equal(neg.length, 10);
  assert.equal(decodeVarint(neg).value, (1n << 64n) - 1n);
});

test('varint: 截断时抛 TruncationError（安全越界防护）', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80])), TruncationError);
  assert.throws(() => decodeVarint(new Uint8Array([0xac])), TruncationError);
  try {
    decodeVarint(new Uint8Array([0x80]));
    assert.unreachable();
  } catch (e) {
    const err = e as TruncationError;
    assert.equal(err.name, 'TruncationError');
    assert.equal(err.offset, 0);
    assert.equal(err.available, 1);
  }
});

test('varint: 超过 10 字节或第 10 字节溢出抛 MalformedError', () => {
  const eleven = new Uint8Array(11).fill(0x80);
  eleven[10] = 0x01;
  assert.throws(() => decodeVarint(eleven), MalformedError);
  const overflow = new Uint8Array(10).fill(0xff);
  assert.throws(() => decodeVarint(overflow), MalformedError);
});

test('zigzag32: 已知映射与全边界往返', () => {
  assert.equal(zigzagEncode32(0), 0);
  assert.equal(zigzagEncode32(-1), 1);
  assert.equal(zigzagEncode32(1), 2);
  assert.equal(zigzagEncode32(-2), 3);
  assert.equal(zigzagEncode32(2147483647), 4294967294);
  assert.equal(zigzagEncode32(-2147483648), 4294967295);
  for (const n of [0, 1, -1, 2, -2, 123456, -123456, 2147483647, -2147483648]) {
    assert.equal(zigzagDecode32(zigzagEncode32(n)), n);
  }
  assert.throws(() => zigzagEncode32(2147483648), RangeError);
});

test('zigzag64: 已知映射与全边界往返', () => {
  assert.equal(zigzagEncode64(0n), 0n);
  assert.equal(zigzagEncode64(-1n), 1n);
  assert.equal(zigzagEncode64(1n), 2n);
  assert.equal(zigzagEncode64(9223372036854775807n), 18446744073709551614n);
  assert.equal(zigzagEncode64(-9223372036854775808n), 18446744073709551615n);
  for (const v of [0n, 1n, -1n, 2n, -2n, 9223372036854775807n, -9223372036854775808n]) {
    assert.equal(zigzagDecode64(zigzagEncode64(v)), v);
  }
});
