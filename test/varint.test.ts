import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint, decodeVarint, varintLength,
  zigzagEncode32, zigzagDecode32, zigzagEncode64, zigzagDecode64, toSigned64,
  WireError,
} from '../src/index.ts';

test('varint round-trip over boundary values', () => {
  const values = [
    0n, 1n, 127n, 128n, 300n, 16383n, 16384n,
    (1n << 31n) - 1n, 1n << 31n, (1n << 32n) - 1n, 1n << 32n,
    (1n << 53n) - 1n, (1n << 63n) - 1n, 1n << 63n, (1n << 64n) - 1n,
  ];
  for (const v of values) {
    const encoded = encodeVarint(v);
    const { value, bytesRead } = decodeVarint(encoded);
    assert.equal(value, v, `round-trip of ${v}`);
    assert.equal(bytesRead, encoded.length);
    assert.equal(varintLength(v), encoded.length);
  }
});

test('varint canonical encodings match protobuf spec', () => {
  assert.deepEqual([...encodeVarint(1)], [0x01]);
  assert.deepEqual([...encodeVarint(127)], [0x7f]);
  assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
  assert.deepEqual([...encodeVarint(150)], [0x96, 0x01]);
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
  assert.equal(encodeVarint((1n << 64n) - 1n).length, 10);
});

test('varint accepts number input', () => {
  assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
});

test('decodeVarint respects offset', () => {
  const buf = new Uint8Array([0xff, 0xac, 0x02, 0xff]);
  const { value, bytesRead } = decodeVarint(buf, 1);
  assert.equal(value, 300n);
  assert.equal(bytesRead, 2);
});

test('decodeVarint throws TRUNCATED on unterminated varint', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80])), (e: unknown) => {
    assert.ok(e instanceof WireError);
    assert.equal((e as WireError).code, 'TRUNCATED');
    return true;
  });
  assert.throws(() => decodeVarint(new Uint8Array([])), /truncated/);
});

test('decodeVarint rejects >64-bit overflow', () => {
  const eleven = new Uint8Array([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01]);
  assert.throws(() => decodeVarint(eleven), (e: unknown) => {
    assert.equal((e as WireError).code, 'VARINT_OVERFLOW');
    return true;
  });
  const tenBad = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
  assert.throws(() => decodeVarint(tenBad), (e: unknown) => {
    assert.equal((e as WireError).code, 'VARINT_OVERFLOW');
    return true;
  });
});

test('zigzag32 boundary values', () => {
  const cases: Array<[number, number]> = [
    [0, 0], [-1, 1], [1, 2], [-2, 3],
    [2147483647, 4294967294], [-2147483648, 4294967295],
  ];
  for (const [n, enc] of cases) {
    assert.equal(zigzagEncode32(n), enc, `encode ${n}`);
    assert.equal(zigzagDecode32(enc), n, `decode ${enc}`);
  }
});

test('zigzag64 boundary values', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n], [-1n, 1n], [1n, 2n], [-2n, 3n],
    [(1n << 63n) - 1n, (1n << 64n) - 2n],
    [-(1n << 63n), (1n << 64n) - 1n],
  ];
  for (const [n, enc] of cases) {
    assert.equal(zigzagEncode64(n), enc, `encode ${n}`);
    assert.equal(zigzagDecode64(enc), n, `decode ${enc}`);
  }
});

test('toSigned64 interprets two-complement', () => {
  assert.equal(toSigned64(0n), 0n);
  assert.equal(toSigned64((1n << 64n) - 1n), -1n);
  assert.equal(toSigned64(1n << 63n), -(1n << 63n));
});
