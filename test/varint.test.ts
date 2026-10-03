import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeVarint,
  encodeVarint,
  zigzagDecode32,
  zigzagDecode64,
  zigzagEncode32,
  zigzagEncode64,
} from '../src/wire/varint.ts';
import { MalformedVarintError, TruncatedFieldError } from '../src/wire/errors.ts';

test('varint roundtrip across magnitudes', () => {
  const values = [
    0n,
    1n,
    127n,
    128n,
    300n,
    16384n,
    (1n << 31n) - 1n,
    1n << 31n,
    (1n << 32n) - 1n,
    1n << 32n,
    (1n << 53n) - 1n,
    (1n << 63n) - 1n,
    1n << 63n,
    (1n << 64n) - 1n,
  ];
  for (const v of values) {
    const encoded = encodeVarint(v);
    const decoded = decodeVarint(encoded, 0);
    assert.equal(decoded.value, v);
    assert.equal(decoded.bytesRead, encoded.length);
  }
});

test('varint known encodings', () => {
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(127n)], [0x7f]);
  assert.deepEqual([...encodeVarint(128n)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
  assert.deepEqual(
    [...encodeVarint((1n << 64n) - 1n)],
    [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01],
  );
});

test('varint decode respects end offset', () => {
  const buf = new Uint8Array([0x99, 0xac, 0x02, 0x77]);
  const { value, bytesRead } = decodeVarint(buf, 1, 3);
  assert.equal(value, 300n);
  assert.equal(bytesRead, 2);
});

test('varint truncated raises TruncatedFieldError', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80]), 0), TruncatedFieldError);
  assert.throws(() => decodeVarint(new Uint8Array([]), 0), TruncatedFieldError);
  try {
    decodeVarint(new Uint8Array([0xff, 0xff]), 0);
    assert.unreachable();
  } catch (err) {
    assert.ok(err instanceof TruncatedFieldError);
    assert.equal(err.offset, 0);
    assert.equal(err.available, 2);
  }
});

test('varint overflow raises MalformedVarintError', () => {
  const elevenContinuations = new Uint8Array(11).fill(0x80);
  assert.throws(() => decodeVarint(elevenContinuations, 0), MalformedVarintError);
  const tenthByteTooBig = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
  assert.throws(() => decodeVarint(tenthByteTooBig, 0), MalformedVarintError);
});

test('zigzag32 known vectors', () => {
  const vectors: Array<[number, number]> = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [signed, unsigned] of vectors) {
    assert.equal(zigzagEncode32(signed), unsigned);
    assert.equal(zigzagDecode32(unsigned), signed);
  }
});

test('zigzag64 known vectors and roundtrip', () => {
  assert.equal(zigzagEncode64(0n), 0n);
  assert.equal(zigzagEncode64(-1n), 1n);
  assert.equal(zigzagEncode64(1n), 2n);
  assert.equal(zigzagEncode64(-2n), 3n);
  assert.equal(zigzagEncode64(-(1n << 63n)), (1n << 64n) - 1n);
  assert.equal(zigzagEncode64((1n << 63n) - 1n), (1n << 64n) - 2n);
  const samples = [0n, 1n, -1n, 2n, -2n, 123456789012345n, -987654321098765n, (1n << 63n) - 1n, -(1n << 63n)];
  for (const s of samples) {
    assert.equal(zigzagDecode64(zigzagEncode64(s)), s);
  }
});
