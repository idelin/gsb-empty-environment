import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint,
  decodeVarint,
  zigzagEncode,
  zigzagDecode,
  encodeFixed32,
  decodeFixed32,
  encodeFixed64,
  decodeFixed64,
  TruncationError,
  WireError,
  MASK_64,
} from '../src/wire.ts';

test('varint round-trips boundary values', () => {
  const cases = [0n, 1n, 127n, 128n, 300n, 16383n, 16384n, 2n ** 32n - 1n, 2n ** 53n, MASK_64];
  for (const v of cases) {
    const enc = encodeVarint(v);
    const { value, next } = decodeVarint(enc, 0, enc.length);
    assert.equal(value, v, `value ${v}`);
    assert.equal(next, enc.length);
  }
});

test('varint canonical encodings match the protobuf spec', () => {
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(127n)], [0x7f]);
  assert.deepEqual([...encodeVarint(128n)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(150n)], [0x96, 0x01]);
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
});

test('varint encodes negative int64 as 10-byte two\'s complement', () => {
  const enc = encodeVarint(-1n);
  assert.equal(enc.length, 10);
  assert.deepEqual([...enc], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]);
  assert.equal(decodeVarint(enc, 0, enc.length).value, MASK_64);
});

test('varint decode respects a mid-buffer limit', () => {
  const buf = Uint8Array.of(0x01, 0xac, 0x02, 0x03);
  const { value, next } = decodeVarint(buf, 1, 3);
  assert.equal(value, 300n);
  assert.equal(next, 3);
});

test('varint truncation raises TruncationError with guard metadata', () => {
  assert.throws(() => decodeVarint(Uint8Array.of(0x80), 0, 1), (err: unknown) => {
    assert.ok(err instanceof TruncationError);
    assert.equal(err.offset, 0);
    assert.equal(err.available, 1);
    return true;
  });
  // continuation bit set on the last available byte
  assert.throws(() => decodeVarint(Uint8Array.of(0xff, 0xff), 0, 2), TruncationError);
});

test('varint overflow beyond 64 bits is rejected', () => {
  // 10th byte carrying more than 1 payload bit
  const bad = Uint8Array.of(0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02);
  assert.throws(() => decodeVarint(bad, 0, bad.length), WireError);
  // 11-byte varint: 10th byte still has the continuation bit
  const long = Uint8Array.of(0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x81, 0x01);
  assert.throws(() => decodeVarint(long, 0, long.length), WireError);
});

test('zigzag mapping follows the spec and round-trips int64 extremes', () => {
  assert.equal(zigzagEncode(0n), 0n);
  assert.equal(zigzagEncode(-1n), 1n);
  assert.equal(zigzagEncode(1n), 2n);
  assert.equal(zigzagEncode(-2n), 3n);
  assert.equal(zigzagEncode(2147483647n), 4294967294n);
  assert.equal(zigzagEncode(-2147483648n), 4294967295n);
  const extremes = [0n, 1n, -1n, 2n ** 62n, -(2n ** 62n), 2n ** 63n - 1n, -(2n ** 63n)];
  for (const v of extremes) {
    assert.equal(zigzagDecode(zigzagEncode(v)), v, `zigzag ${v}`);
  }
});

test('fixed32 round-trips and guards truncation', () => {
  for (const v of [0, 1, 255, 65535, 2 ** 31, 0xffffffff]) {
    const enc = encodeFixed32(v);
    assert.equal(enc.length, 4);
    assert.equal(decodeFixed32(enc, 0, 4).value, v >>> 0);
  }
  assert.throws(() => decodeFixed32(Uint8Array.of(1, 2, 3), 0, 3), TruncationError);
});

test('fixed64 round-trips little-endian and guards truncation', () => {
  const cases = [0n, 1n, 0xdeadbeefn, MASK_64, 0x0102030405060708n];
  for (const v of cases) {
    const enc = encodeFixed64(v);
    assert.equal(enc.length, 8);
    assert.equal(decodeFixed64(enc, 0, 8).value, v);
  }
  assert.deepEqual([...encodeFixed64(1n)], [1, 0, 0, 0, 0, 0, 0, 0]);
  assert.throws(() => decodeFixed64(Uint8Array.of(1, 2, 3, 4, 5, 6, 7), 0, 7), TruncationError);
});
