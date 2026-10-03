import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeFixed32,
  decodeFixed64,
  decodeVarint,
  encodeFixed32,
  encodeFixed64,
  encodeVarint,
  TruncatedFieldError,
  VarintOverflowError,
  zigzagDecode,
  zigzagEncode,
} from '../src/varint.ts';

test('varint round-trips boundary values', () => {
  const values = [
    0n,
    1n,
    127n, // largest 1-byte varint
    128n, // smallest 2-byte varint
    300n, // canonical protobuf example
    16383n, // largest 2-byte varint
    16384n,
    2n ** 32n - 1n,
    2n ** 32n,
    2n ** 53n - 1n, // max safe JS integer
    2n ** 63n - 1n, // max int64
    2n ** 64n - 1n, // max uint64
  ];
  for (const v of values) {
    const encoded = encodeVarint(v);
    const { value, bytesRead } = decodeVarint(encoded, 0);
    assert.equal(value, v, `round-trip of ${v}`);
    assert.equal(bytesRead, encoded.length);
  }
});

test('varint uses exactly 7 bits per byte with continuation bit', () => {
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
  assert.deepEqual([...encodeVarint(0n)], [0x00]);
  assert.deepEqual([...encodeVarint(127n)], [0x7f]);
  assert.deepEqual([...encodeVarint(128n)], [0x80, 0x01]);
  // max uint64 -> 10 bytes, last byte carries exactly 1 bit
  const max = encodeVarint(2n ** 64n - 1n);
  assert.equal(max.length, 10);
  assert.equal(max[9], 0x01);
});

test('varint decodes from a non-zero offset', () => {
  const buf = new Uint8Array([0xff, 0xac, 0x02, 0xff]);
  const { value, bytesRead } = decodeVarint(buf, 1);
  assert.equal(value, 300n);
  assert.equal(bytesRead, 2);
});

test('varint reports truncation instead of reading out of bounds', () => {
  const buf = new Uint8Array([0x80]); // continuation bit set, no next byte
  assert.throws(() => decodeVarint(buf, 0), TruncatedFieldError);
  assert.throws(() => decodeVarint(new Uint8Array([0x80, 0x80]), 0), TruncatedFieldError);
});

test('varint rejects encodings longer than 64 bits', () => {
  // 10 continuation bytes + 1 more = overflow
  const tooLong = new Uint8Array(11).fill(0x80);
  tooLong[10] = 0x01;
  assert.throws(() => decodeVarint(tooLong, 0), VarintOverflowError);
  // 10th byte carrying more than 1 bit is also overflow
  const excessBits = new Uint8Array(10).fill(0xff);
  assert.throws(() => decodeVarint(excessBits, 0), VarintOverflowError);
});

test('zigzag maps signed values to compact unsigned ones', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2n, 4n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [2n ** 63n - 1n, 2n ** 64n - 2n],
    [-(2n ** 63n), 2n ** 64n - 1n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode(signed), unsigned, `encode ${signed}`);
    assert.equal(zigzagDecode(unsigned), signed, `decode ${unsigned}`);
  }
});

test('fixed32/fixed64 little-endian round-trip and truncation', () => {
  assert.deepEqual([...encodeFixed32(0xdeadbeef)], [0xef, 0xbe, 0xad, 0xde]);
  assert.equal(decodeFixed32(encodeFixed32(0xdeadbeef), 0), 0xdeadbeef);

  const big = 0x0123456789abcdefn;
  assert.deepEqual(
    [...encodeFixed64(big)],
    [0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01],
  );
  assert.equal(decodeFixed64(encodeFixed64(big), 0), big);

  assert.throws(() => decodeFixed32(new Uint8Array(3), 0), TruncatedFieldError);
  assert.throws(() => decodeFixed64(new Uint8Array(7), 0), TruncatedFieldError);
  assert.throws(() => decodeFixed32(new Uint8Array(4), 1), TruncatedFieldError);
});
