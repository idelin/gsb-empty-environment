import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  asSigned64,
  decodeVarint,
  encodeVarint,
  varintLength,
  zigZagDecode32,
  zigZagDecode64,
  zigZagEncode32,
  zigZagEncode64,
} from '../src/varint.ts';
import { ProtobufDecodeError, TruncatedMessageError } from '../src/errors.ts';

test('varint round-trips boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 16383n, 16384n, 2n ** 32n - 1n, 2n ** 63n, 2n ** 64n - 1n];
  for (const v of values) {
    const encoded = encodeVarint(v);
    const { value, bytesRead } = decodeVarint(encoded);
    assert.equal(value, v);
    assert.equal(bytesRead, encoded.length);
    assert.equal(bytesRead, varintLength(v));
  }
});

test('varint 7-bit compression matches canonical encodings', () => {
  assert.deepEqual([...encodeVarint(0n)], [0x00]);
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(127n)], [0x7f]);
  assert.deepEqual([...encodeVarint(128n)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]); // protobuf doc example
  assert.equal(encodeVarint(2n ** 64n - 1n).length, 10);
});

test('negative int64 masks to 10-byte two-complement form', () => {
  const encoded = encodeVarint(-1n);
  assert.equal(encoded.length, 10);
  assert.equal(asSigned64(decodeVarint(encoded).value), -1n);
});

test('varint decoder guards against truncation at every cut point', () => {
  const full = encodeVarint(2n ** 64n - 1n); // 10 bytes
  for (let cut = 0; cut < full.length; cut += 1) {
    assert.throws(() => decodeVarint(full.subarray(0, cut)), TruncatedMessageError);
  }
});

test('varint decoder rejects >64-bit overflow', () => {
  const overflow = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
  assert.throws(() => decodeVarint(overflow), ProtobufDecodeError);
});

test('zigzag32 maps signed to unsigned and back', () => {
  const cases: [number, number][] = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2, 4],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigZagEncode32(signed), unsigned);
    assert.equal(zigZagDecode32(unsigned), signed);
  }
});

test('zigzag64 round-trips int64 extremes', () => {
  const cases: [bigint, bigint][] = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [9223372036854775807n, 18446744073709551614n],
    [-9223372036854775808n, 18446744073709551615n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigZagEncode64(signed), unsigned);
    assert.equal(zigZagDecode64(unsigned), signed);
  }
});
