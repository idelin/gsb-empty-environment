import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeVarint,
  encodeVarint,
  varintLength,
  zigzagDecode32,
  zigzagDecode64,
  zigzagEncode32,
  zigzagEncode64,
} from '../src/varint.ts';
import { ProtobufDecodeError } from '../src/errors.ts';

const VECTORS: Array<[bigint, number[]]> = [
  [0n, [0x00]],
  [1n, [0x01]],
  [127n, [0x7f]],
  [128n, [0x80, 0x01]],
  [300n, [0xac, 0x02]],
  [4294967295n, [0xff, 0xff, 0xff, 0xff, 0x0f]],
  [4294967296n, [0x80, 0x80, 0x80, 0x80, 0x10]],
  [0xffffffffffffffffn, [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]],
];

test('varint golden vectors', () => {
  for (const [value, bytes] of VECTORS) {
    assert.deepEqual([...encodeVarint(value)], bytes, `encode ${value}`);
    const { value: decoded, offset } = decodeVarint(new Uint8Array(bytes), 0, bytes.length);
    assert.equal(decoded, value, `decode ${value}`);
    assert.equal(offset, bytes.length);
    assert.equal(varintLength(value), bytes.length);
  }
});

test('negative int64 encodes as 10-byte two-complement varint', () => {
  const bytes = encodeVarint(-1n);
  assert.deepEqual([...bytes], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]);
  const { value } = decodeVarint(bytes, 0, bytes.length);
  assert.equal(BigInt.asIntN(64, value), -1n);
});

test('varint round-trip over edge and pseudo-random 64-bit values', () => {
  const values: bigint[] = [0n, 1n, 2n ** 32n - 1n, 2n ** 63n - 1n, -(2n ** 63n), -1n];
  let seed = 0x12345678n;
  for (let i = 0; i < 5000; i += 1) {
    seed = (seed * 6364136223846793005n + 1442695040888963407n) & 0xffffffffffffffffn;
    values.push(BigInt.asIntN(64, seed));
  }
  for (const v of values) {
    const bytes = encodeVarint(v);
    const { value, offset } = decodeVarint(bytes, 0, bytes.length);
    assert.equal(offset, bytes.length);
    assert.equal(BigInt.asIntN(64, value), v);
  }
});

test('varint decode respects a non-zero window offset', () => {
  const buf = new Uint8Array([0x99, 0x99, 0xac, 0x02, 0x77]);
  const { value, offset } = decodeVarint(buf, 2, 5);
  assert.equal(value, 300n);
  assert.equal(offset, 4);
});

test('truncated varint reports TRUNCATED', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80]), 0, 1), (e: unknown) => {
    assert.ok(e instanceof ProtobufDecodeError);
    assert.equal((e as ProtobufDecodeError).code, 'TRUNCATED');
    return true;
  });
  assert.throws(
    () => decodeVarint(new Uint8Array([0xff, 0xff, 0xff]), 0, 3),
    (e: unknown) => (e as ProtobufDecodeError).code === 'TRUNCATED',
  );
});

test('varint longer than 10 bytes reports VARINT_OVERFLOW', () => {
  const eleven = new Uint8Array(11).fill(0x80);
  eleven[10] = 0x01;
  assert.throws(
    () => decodeVarint(eleven, 0, eleven.length),
    (e: unknown) => (e as ProtobufDecodeError).code === 'VARINT_OVERFLOW',
  );
});

test('10th varint byte greater than 1 reports VARINT_OVERFLOW', () => {
  const bad = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
  assert.throws(
    () => decodeVarint(bad, 0, bad.length),
    (e: unknown) => (e as ProtobufDecodeError).code === 'VARINT_OVERFLOW',
  );
});

test('zigzag32 golden vectors and round-trip', () => {
  const cases: Array<[number, number]> = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode32(signed), unsigned);
    assert.equal(zigzagDecode32(unsigned), signed);
  }
  let x = 1;
  for (let i = 0; i < 100000; i += 1) {
    x = (x * 1103515245 + 12345) | 0;
    assert.equal(zigzagDecode32(zigzagEncode32(x)), x);
  }
});

test('zigzag64 golden vectors and round-trip', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [9223372036854775807n, 18446744073709551614n],
    [-9223372036854775808n, 18446744073709551615n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode64(signed), unsigned);
    assert.equal(zigzagDecode64(unsigned), signed);
  }
  let x = 7n;
  for (let i = 0; i < 100000; i += 1) {
    x = BigInt.asIntN(64, x * 2862933555777941757n + 3037000493n);
    assert.equal(zigzagDecode64(zigzagEncode64(x)), x);
  }
});
