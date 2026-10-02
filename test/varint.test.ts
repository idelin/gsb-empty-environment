import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BufferReader, BufferWriter, DecodeError } from '../src/index.ts';
import { zigZagEncode32, zigZagDecode32, zigZagEncode64, zigZagDecode64 } from '../src/index.ts';

test('varint round-trips boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 0x7fffn, 0xffffn, 0x7fffffffn, 0xffffffffn,
    0xffffffffffn, 0x7fffffffffffffffn, 0xffffffffffffffffn];
  for (const v of values) {
    const w = new BufferWriter().writeVarint(v);
    const r = new BufferReader(w.finish());
    assert.equal(r.readVarint(), v, `value ${v}`);
    assert.ok(r.done);
  }
});

test('varint canonical encodings match protobuf spec', () => {
  assert.deepEqual([...new BufferWriter().writeVarint(1n).finish()], [0x01]);
  assert.deepEqual([...new BufferWriter().writeVarint(127n).finish()], [0x7f]);
  assert.deepEqual([...new BufferWriter().writeVarint(128n).finish()], [0x80, 0x01]);
  assert.deepEqual([...new BufferWriter().writeVarint(300n).finish()], [0xac, 0x02]);
  assert.deepEqual(
    [...new BufferWriter().writeVarint(0xffffffffffffffffn).finish()],
    [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01],
  );
});

test('varint of 11 continuation bytes reports overflow', () => {
  const buf = Uint8Array.from([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f, 0x01]);
  assert.throws(() => new BufferReader(buf).readVarint(), (e: unknown) => {
    assert.ok(e instanceof DecodeError);
    assert.equal((e as DecodeError).code, 'ERR_VARINT_OVERFLOW');
    return true;
  });
});

test('truncated varint reports ERR_TRUNCATED with offset', () => {
  const buf = Uint8Array.from([0x80, 0x80]);
  assert.throws(() => new BufferReader(buf).readVarint(), (e: unknown) => {
    assert.equal((e as DecodeError).code, 'ERR_TRUNCATED');
    assert.equal((e as DecodeError).offset, 2);
    return true;
  });
});

test('zigzag32 known vectors', () => {
  const cases: [number, number][] = [
    [0, 0], [-1, 1], [1, 2], [-2, 3],
    [2147483647, 4294967294], [-2147483648, 4294967295],
  ];
  for (const [decoded, encoded] of cases) {
    assert.equal(zigZagEncode32(decoded), encoded);
    assert.equal(zigZagDecode32(encoded), decoded);
  }
});

test('zigzag64 known vectors and round-trip', () => {
  assert.equal(zigZagEncode64(0n), 0n);
  assert.equal(zigZagEncode64(-1n), 1n);
  assert.equal(zigZagEncode64(1n), 2n);
  assert.equal(zigZagEncode64(9223372036854775807n), 0xfffffffffffffffen);
  assert.equal(zigZagEncode64(-9223372036854775808n), 0xffffffffffffffffn);
  for (const v of [0n, 1n, -1n, 123456789012345n, -123456789012345n, 9223372036854775807n, -9223372036854775808n]) {
    assert.equal(zigZagDecode64(zigZagEncode64(v)), v);
  }
});

test('fixed32/fixed64 little-endian round-trip and truncation guard', () => {
  const w = new BufferWriter().writeFixed32(0xdeadbeef).writeFixed64(0x0102030405060708n);
  const r = new BufferReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);

  assert.throws(() => new BufferReader(Uint8Array.from([1, 2, 3])).readFixed32(),
    (e: unknown) => (e as DecodeError).code === 'ERR_TRUNCATED');
  assert.throws(() => new BufferReader(Uint8Array.from([1, 2, 3, 4, 5])).readFixed64(),
    (e: unknown) => (e as DecodeError).code === 'ERR_TRUNCATED');
});

test('float/double round-trip', () => {
  const w = new BufferWriter().writeFloat(1.5).writeDouble(-3.141592653589793);
  const r = new BufferReader(w.finish());
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readDouble(), -3.141592653589793);
});

test('reader window constructor rejects out-of-range views', () => {
  const buf = Uint8Array.from([1, 2, 3]);
  assert.throws(() => new BufferReader(buf, 2, 5), (e: unknown) => (e as DecodeError).code === 'ERR_TRUNCATED');
  assert.throws(() => new BufferReader(buf, -1), (e: unknown) => (e as DecodeError).code === 'ERR_TRUNCATED');
});

test('release drops buffer reference (leak guard)', () => {
  const r = new BufferReader(Uint8Array.from([1]));
  assert.equal(r.released, false);
  r.release();
  assert.equal(r.released, true);
});
