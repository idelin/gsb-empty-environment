import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ProtobufDecodeError,
  decodeVarint,
  encodeVarint,
  varintByteLength,
  zigzagDecode,
  zigzagDecode32,
  zigzagEncode,
  zigzagEncode32,
  toSigned64,
} from '../src/encoding.ts';

test('varint: canonical encodings match protobuf spec', () => {
  assert.deepEqual([...encodeVarint(1)], [0x01]);
  assert.deepEqual([...encodeVarint(127)], [0x7f]);
  assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
  assert.deepEqual([...encodeVarint(150)], [0x96, 0x01]);
});

test('varint: roundtrip across 7-bit boundaries', () => {
  const values = [
    0n,
    1n,
    127n,
    128n,
    300n,
    16383n,
    16384n,
    0x7fffffffn,
    0x80000000n,
    0xffffffffn,
    0x100000000n,
    0x7fffffffffffffffn,
    0xffffffffffffffffn,
  ];
  for (const v of values) {
    const enc = encodeVarint(v);
    assert.equal(enc.length, varintByteLength(v));
    const { value, bytesRead } = decodeVarint(enc);
    assert.equal(value, v & 0xffffffffffffffffn);
    assert.equal(bytesRead, enc.length);
  }
});

test('varint: negative int64 encodes as 10-byte two-complement', () => {
  const enc = encodeVarint(toSigned64(0xffffffffffffffffn) & 0xffffffffffffffffn);
  assert.equal(enc.length, 10);
  const { value } = decodeVarint(enc);
  assert.equal(toSigned64(value), -1n);
});

test('varint: truncated buffer raises ProtobufDecodeError', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80])), ProtobufDecodeError);
  assert.throws(
    () => decodeVarint(new Uint8Array([0x80, 0x80, 0x80])),
    /truncated varint/,
  );
  assert.throws(() => decodeVarint(new Uint8Array([])), ProtobufDecodeError);
});

test('varint: rejects overlong and overflowing sequences', () => {
  const eleven = new Uint8Array(11).fill(0x80);
  eleven[10] = 0x01;
  assert.throws(() => decodeVarint(eleven), ProtobufDecodeError);
  const overflow10th = new Uint8Array(10).fill(0xff);
  assert.throws(() => decodeVarint(overflow10th), /overflows 64 bits/);
});

test('varint: respects explicit limit window', () => {
  const buf = new Uint8Array([0x96, 0x01, 0xff, 0xff]);
  assert.throws(() => decodeVarint(buf, 2, 3), ProtobufDecodeError);
  const { value } = decodeVarint(buf, 0, 2);
  assert.equal(value, 150n);
});

test('zigzag: spec-mandated mapping', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [9223372036854775807n, 18446744073709551614n],
    [-9223372036854775808n, 18446744073709551615n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode(signed), unsigned);
    assert.equal(zigzagDecode(unsigned), signed);
  }
});

test('zigzag: 32-bit variant roundtrips int32 extremes', () => {
  for (const v of [0, -1, 1, -2, 2, 2147483647, -2147483648, 123456789, -987654321]) {
    assert.equal(zigzagDecode32(zigzagEncode32(v)), v);
  }
  assert.equal(zigzagEncode32(-1), 1);
  assert.equal(zigzagEncode32(1), 2);
});

test('toSigned64: interprets raw 64-bit patterns', () => {
  assert.equal(toSigned64(0n), 0n);
  assert.equal(toSigned64(0xffffffffffffffffn), -1n);
  assert.equal(toSigned64(0x8000000000000000n), -9223372036854775808n);
  assert.equal(toSigned64(0x7fffffffffffffffn), 9223372036854775807n);
});
