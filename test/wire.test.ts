import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DecodeError,
  ErrorCode,
  WireReader,
  WireType,
  WireWriter,
  zigZagDecode32,
  zigZagDecode64,
  zigZagEncode32,
  zigZagEncode64,
} from '../src/wire.ts';

test('varint roundtrip across boundary values', () => {
  const values = [0n, 1n, 127n, 128n, 300n, 16383n, 16384n, 2n ** 31n - 1n, 2n ** 32n, 2n ** 53n - 1n, 2n ** 64n - 1n];
  for (const v of values) {
    const w = new WireWriter().varint(v);
    const r = new WireReader(w.finish());
    assert.equal(r.readVarint64(), v, `roundtrip ${v}`);
    assert.ok(r.done);
  }
});

test('varint minimal-length encoding (7-bit compression)', () => {
  assert.deepEqual([...new WireWriter().varint(0).finish()], [0x00]);
  assert.deepEqual([...new WireWriter().varint(127).finish()], [0x7f]);
  assert.deepEqual([...new WireWriter().varint(128).finish()], [0x80, 0x01]);
  assert.deepEqual([...new WireWriter().varint(300).finish()], [0xac, 0x02]);
  assert.equal(new WireWriter().varint(2n ** 64n - 1n).finish().length, 10);
});

test('zigzag encode/decode mapping', () => {
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
  const big: [bigint, bigint][] = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-(2n ** 63n), 2n ** 64n - 1n],
    [2n ** 63n - 1n, 2n ** 64n - 2n],
  ];
  for (const [s, u] of big) {
    assert.equal(zigZagEncode64(s), u);
    assert.equal(zigZagDecode64(u), s);
  }
});

test('zigzag via reader/writer for sint32/sint64', () => {
  const w = new WireWriter().zigZag32(-12345).zigZag64(-(2n ** 60n));
  const r = new WireReader(w.finish());
  assert.equal(r.readSInt32(), -12345);
  assert.equal(r.readSInt64(), -(2n ** 60n));
  assert.ok(r.done);
});

test('fixed32/fixed64 little-endian roundtrip', () => {
  const w = new WireWriter().fixed32(0xdeadbeef).fixed64(0x0102030405060708n);
  const bytes = w.finish();
  assert.deepEqual([...bytes.slice(0, 4)], [0xef, 0xbe, 0xad, 0xde]);
  assert.deepEqual([...bytes.slice(4)], [8, 7, 6, 5, 4, 3, 2, 1]);
  const r = new WireReader(bytes);
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0102030405060708n);
});

test('float/double roundtrip including specials', () => {
  const w = new WireWriter().float(1.5).double(-0.0).float(NaN);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 1.5);
  assert.ok(Object.is(r.readDouble(), -0));
  assert.ok(Number.isNaN(r.readFloat()));
});

test('truncated varint raises ERR_TRUNCATED', () => {
  const r = new WireReader(Uint8Array.from([0x80])); // continuation bit, no next byte
  assert.throws(() => r.readVarint64(), (e: unknown) => {
    assert.ok(e instanceof DecodeError);
    assert.equal((e as DecodeError).code, ErrorCode.TRUNCATED);
    return true;
  });
});

test('varint longer than 10 bytes raises ERR_INVALID_VARINT', () => {
  const bytes = Uint8Array.from([...Array(10).fill(0xff), 0x01]);
  const r = new WireReader(bytes);
  assert.throws(() => r.readVarint64(), (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_VARINT);
});

test('varint 10th byte overflowing 64 bits raises ERR_INVALID_VARINT', () => {
  const bytes = Uint8Array.from([...Array(9).fill(0xff), 0x7f]);
  const r = new WireReader(bytes);
  assert.throws(() => r.readVarint64(), (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_VARINT);
});

test('truncated fixed32/fixed64 raise ERR_TRUNCATED', () => {
  assert.throws(
    () => new WireReader(Uint8Array.from([1, 2, 3])).readFixed32(),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
  assert.throws(
    () => new WireReader(Uint8Array.from([1, 2, 3, 4, 5])).readFixed64(),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
});

test('length-delimited field overrun raises ERR_TRUNCATED', () => {
  const w = new WireWriter().varint(100).byte(1).byte(2); // declares 100, provides 2
  const r = new WireReader(w.finish());
  assert.throws(() => r.readBytes(), (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED);
});

test('readBytes returns a copy, not an alias of the source buffer', () => {
  const src = new WireWriter().bytes(Uint8Array.from([1, 2, 3])).finish();
  const r = new WireReader(src);
  const out = r.readBytes();
  src[1] = 0xff;
  assert.equal(out[0], 1);
});

test('invalid tag: field number zero and reserved wire types', () => {
  assert.throws(
    () => new WireReader(Uint8Array.from([0x00])).readTag(),
    (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_TAG,
  );
  assert.throws(
    () => new WireReader(Uint8Array.from([0x0f])).readTag(), // field 1, wire 7
    (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_WIRE_TYPE,
  );
});

test('skipField handles every wire type and truncation', () => {
  const w = new WireWriter()
    .varint(150)
    .fixed64(1n)
    .bytes(Uint8Array.from([9, 9]))
    .fixed32(7);
  const r = new WireReader(w.finish());
  r.skipField(WireType.VARINT);
  r.skipField(WireType.I64);
  r.skipField(WireType.LEN);
  r.skipField(WireType.I32);
  assert.ok(r.done);

  const truncated = new WireReader(Uint8Array.from([5, 1])); // len=5, 1 byte present
  assert.throws(
    () => truncated.skipField(WireType.LEN),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
});

test('int32/int64 reinterpret varint as two complement', () => {
  const w = new WireWriter().varint(-1); // encodes as 10-byte uint64 max
  const r = new WireReader(w.finish());
  assert.equal(r.readInt64(), -1n);

  const w2 = new WireWriter().varint(0xffffffffn);
  assert.equal(new WireReader(w2.finish()).readInt32(), -1);
});

test('reader window constructor rejects out-of-bounds ranges', () => {
  const buf = Uint8Array.from([1, 2, 3]);
  assert.throws(() => new WireReader(buf, 2, 1));
  assert.throws(() => new WireReader(buf, 0, 4));
});
