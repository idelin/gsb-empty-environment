import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeVarint, encodeVarint, zigzagDecode, zigzagEncode } from '../src/varint.ts';
import { DecodeError, EncodeError } from '../src/errors.ts';

test('varint roundtrips boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 2n ** 21n - 1n, 2n ** 32n, 2n ** 63n - 1n, 2n ** 64n - 1n];
  for (const v of values) {
    const enc = encodeVarint(v);
    const dec = decodeVarint(enc, 0);
    assert.equal(dec.value, v, `roundtrip ${v}`);
    assert.equal(dec.bytesRead, enc.length);
  }
});

test('varint 7-bit compression sizes', () => {
  assert.equal(encodeVarint(0n).length, 1);
  assert.equal(encodeVarint(127n).length, 1);
  assert.equal(encodeVarint(128n).length, 2);
  assert.equal(encodeVarint(2n ** 64n - 1n).length, 10);
});

test('varint canonical encodings match protobuf spec', () => {
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(150n)], [0x96, 0x01]); // classic example
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
});

test('negative int64 encodes as 10-byte two\'s complement', () => {
  const enc = encodeVarint(-1n);
  assert.equal(enc.length, 10);
  assert.equal(decodeVarint(enc, 0).value, 2n ** 64n - 1n);
});

test('varint decode respects offset', () => {
  const buf = new Uint8Array([0xff, 0x96, 0x01, 0xff]);
  const dec = decodeVarint(buf, 1);
  assert.equal(dec.value, 150n);
  assert.equal(dec.bytesRead, 2);
});

test('truncated varint throws DecodeError with offset', () => {
  const buf = new Uint8Array([0x80]); // continuation bit set, no next byte
  assert.throws(() => decodeVarint(buf, 0), (e: unknown) => {
    assert.ok(e instanceof DecodeError);
    assert.match((e as Error).message, /truncated/);
    return true;
  });
});

test('varint overflow beyond 64 bits is rejected', () => {
  const tooLong = new Uint8Array(11).fill(0x80);
  tooLong[10] = 0x00;
  assert.throws(() => decodeVarint(tooLong, 0), DecodeError);
  // 10th byte carrying more than 1 payload bit
  const overflow = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
  assert.throws(() => decodeVarint(overflow, 0), /exceeds 64 bits/);
});

test('varint encode rejects out-of-range values', () => {
  assert.throws(() => encodeVarint(2n ** 64n), EncodeError);
  assert.throws(() => encodeVarint(-(2n ** 63n) - 1n), EncodeError);
});

test('zigzag mapping matches protobuf spec', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n], [-1n, 1n], [1n, 2n], [-2n, 3n], [2n, 4n],
    [2147483647n, 4294967294n], [-2147483648n, 4294967295n],
    [(1n << 63n) - 1n, (1n << 64n) - 2n], [-(1n << 63n), (1n << 64n) - 1n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode(signed), unsigned, `encode ${signed}`);
    assert.equal(zigzagDecode(unsigned), signed, `decode ${unsigned}`);
  }
});

test('zigzag roundtrips random int64 values', () => {
  for (let i = 0; i < 1000; i++) {
    const v = BigInt.asIntN(64, BigInt(Math.floor(Math.random() * 2 ** 53)) * (i % 2 ? -1n : 1n));
    assert.equal(zigzagDecode(zigzagEncode(v)), v);
  }
});
