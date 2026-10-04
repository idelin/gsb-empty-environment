import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  readVarint,
  writeVarint,
  varintLength,
  zigzagEncode,
  zigzagDecode,
  toSigned64,
  toUnsigned64,
  MAX_VARINT_BYTES,
} from '../src/wire/varint.ts';
import { ProtobufDecodeError } from '../src/wire/errors.ts';

test('varint round-trips boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 16383n, 16384n, 2n ** 31n - 1n, 2n ** 32n, 2n ** 53n - 1n, 2n ** 64n - 1n];
  for (const v of values) {
    const encoded = writeVarint(v);
    const { value, offset } = readVarint(encoded, 0);
    assert.equal(value, v, `round-trip of ${v}`);
    assert.equal(offset, encoded.length);
    assert.equal(encoded.length, varintLength(v));
  }
});

test('varint 7-bit compression: canonical byte patterns', () => {
  assert.deepEqual([...writeVarint(0)], [0x00]);
  assert.deepEqual([...writeVarint(1)], [0x01]);
  assert.deepEqual([...writeVarint(127)], [0x7f]);
  assert.deepEqual([...writeVarint(128)], [0x80, 0x01]);
  assert.deepEqual([...writeVarint(300)], [0xac, 0x02]); // protobuf doc example
  assert.deepEqual([...writeVarint(2n ** 64n - 1n)], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]);
});

test('varint reads at a non-zero offset inside a larger buffer', () => {
  const buf = Uint8Array.from([0xaa, 0xbb, 0xac, 0x02, 0xcc]);
  const { value, offset } = readVarint(buf, 2);
  assert.equal(value, 300n);
  assert.equal(offset, 4);
});

test('varint rejects truncation at every prefix length', () => {
  const full = writeVarint(2n ** 64n - 1n); // 10 bytes
  for (let len = 0; len < full.length - 1; len++) {
    assert.throws(() => readVarint(full.subarray(0, len), 0), ProtobufDecodeError);
  }
});

test('varint rejects more than 10 bytes (64-bit overflow guard)', () => {
  const tooLong = Uint8Array.from([...Array(MAX_VARINT_BYTES).fill(0x80), 0x01]);
  assert.throws(() => readVarint(tooLong, 0), /exceeds 64 bits/);
});

test('varint rejects 10th byte payload > 1', () => {
  const bad = Uint8Array.from([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
  assert.throws(() => readVarint(bad, 0), ProtobufDecodeError);
});

test('writeVarint rejects negative input', () => {
  assert.throws(() => writeVarint(-1n), RangeError);
});

test('zigzag round-trips signed values including extremes', () => {
  const values = [0n, -1n, 1n, -2n, 2n, 2147483647n, -2147483648n, 2n ** 63n - 1n, -(2n ** 63n)];
  for (const v of values) {
    assert.equal(zigzagDecode(zigzagEncode(v)), v, `zigzag round-trip of ${v}`);
  }
});

test('zigzag canonical mapping (protobuf spec)', () => {
  assert.equal(zigzagEncode(0), 0n);
  assert.equal(zigzagEncode(-1), 1n);
  assert.equal(zigzagEncode(1), 2n);
  assert.equal(zigzagEncode(-2), 3n);
  assert.equal(zigzagEncode(2147483647), 4294967294n);
  assert.equal(zigzagEncode(-2147483648), 4294967295n);
});

test('signed/unsigned 64-bit reinterpretation', () => {
  assert.equal(toSigned64(2n ** 64n - 1n), -1n);
  assert.equal(toSigned64(2n ** 63n), -(2n ** 63n));
  assert.equal(toUnsigned64(-1n), 2n ** 64n - 1n);
  assert.equal(toUnsigned64(toSigned64(123n)), 123n);
});
