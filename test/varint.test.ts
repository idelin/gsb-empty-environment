import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint,
  encodeVarintSigned,
  decodeVarint,
  zigZagEncode32,
  zigZagDecode32,
  zigZagEncode64,
  zigZagDecode64,
  MAX_VARINT_BYTES,
} from '../src/varint.ts';
import { MalformedMessageError, TruncatedMessageError } from '../src/errors.ts';
import { WireReader } from '../src/reader.ts';
import { WireWriter } from '../src/writer.ts';

const U64_MAX = (1n << 64n) - 1n;

test('varint: canonical boundary values round-trip', () => {
  const cases: bigint[] = [
    0n,
    1n,
    127n, // 7-bit boundary
    128n, // first 2-byte value
    300n, // protobuf doc example
    16383n, // 14-bit boundary
    16384n,
    (1n << 21n) - 1n,
    1n << 21n,
    (1n << 32n) - 1n,
    1n << 32n,
    BigInt(Number.MAX_SAFE_INTEGER),
    (1n << 63n) - 1n,
    1n << 63n,
    U64_MAX, // 10-byte maximum
  ];
  for (const v of cases) {
    const encoded = encodeVarint(v);
    const { value, bytesRead } = decodeVarint(encoded, 0);
    assert.equal(value, v, `round-trip ${v}`);
    assert.equal(bytesRead, encoded.length);
  }
});

test('varint: byte-length boundaries are exactly ceil(bits/7)', () => {
  assert.equal(encodeVarint(0n).length, 1);
  assert.equal(encodeVarint(127n).length, 1);
  assert.equal(encodeVarint(128n).length, 2);
  assert.equal(encodeVarint((1n << 63n) - 1n).length, 9);
  assert.equal(encodeVarint(1n << 63n).length, 10);
  assert.equal(encodeVarint(U64_MAX).length, MAX_VARINT_BYTES);
});

test('varint: known byte patterns (protobuf doc vectors)', () => {
  assert.deepEqual([...encodeVarint(1n)], [0x01]);
  assert.deepEqual([...encodeVarint(300n)], [0xac, 0x02]);
  assert.deepEqual([...encodeVarint(0n)], [0x00]);
});

test('varint: decode at non-zero offset respects window', () => {
  const buf = Uint8Array.of(0xff, 0xac, 0x02, 0xff);
  const { value, bytesRead } = decodeVarint(buf, 1);
  assert.equal(value, 300n);
  assert.equal(bytesRead, 2);
});

test('varint: truncated buffer throws TruncatedMessageError, never reads OOB', () => {
  const full = encodeVarint(U64_MAX); // 10 bytes, all continuation bits until last
  for (let len = 1; len < full.length; len++) {
    assert.throws(() => decodeVarint(full.subarray(0, len), 0), TruncatedMessageError);
  }
});

test('varint: 11-byte run is malformed, 10th byte > 1 overflows', () => {
  const eleven = new Uint8Array(11).fill(0x80);
  eleven[10] = 0x01;
  assert.throws(() => decodeVarint(eleven, 0), MalformedMessageError);
  const overflow = new Uint8Array(10).fill(0xff);
  overflow[9] = 0x7f; // 10th byte carries more than 1 bit
  assert.throws(() => decodeVarint(overflow, 0), MalformedMessageError);
});

test('varint: encodeVarint rejects negative input', () => {
  assert.throws(() => encodeVarint(-1n), RangeError);
});

test('varint: signed two-complement encoding (negative int32 -> 10 bytes)', () => {
  const encoded = encodeVarintSigned(-1);
  assert.equal(encoded.length, 10);
  const { value } = decodeVarint(encoded, 0);
  assert.equal(BigInt.asIntN(64, value), -1n);
  assert.equal(BigInt.asIntN(32, value), -1n);
});

test('zigzag32: spec-mandated mapping and boundaries', () => {
  const mapping: Array<[number, number]> = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2, 4],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [signed, unsigned] of mapping) {
    assert.equal(zigZagEncode32(signed), unsigned);
    assert.equal(zigZagDecode32(unsigned), signed);
  }
});

test('zigzag64: spec-mandated mapping and boundaries', () => {
  const max = (1n << 63n) - 1n;
  const min = -(1n << 63n);
  const mapping: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [max, U64_MAX - 1n],
    [min, U64_MAX],
  ];
  for (const [signed, unsigned] of mapping) {
    assert.equal(zigZagEncode64(signed), unsigned);
    assert.equal(zigZagDecode64(unsigned), signed);
  }
});

test('zigzag: exhaustive round-trip over dense range', () => {
  for (let i = -10000; i <= 10000; i++) {
    assert.equal(zigZagDecode32(zigZagEncode32(i)), i);
    const b = BigInt(i);
    assert.equal(zigZagDecode64(zigZagEncode64(b)), b);
  }
});

test('fixed32/64, float/double: writer/reader round-trip', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef);
  w.writeFixed32(0);
  w.writeFixed64(0x0123456789abcdefn);
  w.writeFloat(3.5);
  w.writeDouble(-2.718281828459045);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed32(), 0);
  assert.equal(r.readFixed64(), 0x0123456789abcdefn);
  assert.equal(r.readFloat(), 3.5);
  assert.equal(r.readDouble(), -2.718281828459045);
  assert.ok(r.done);
});

test('reader: fixed-width reads past end throw TruncatedMessageError', () => {
  for (const [size, read] of [
    [4, (r: WireReader) => r.readFixed32()],
    [8, (r: WireReader) => r.readFixed64()],
    [4, (r: WireReader) => r.readFloat()],
    [8, (r: WireReader) => r.readDouble()],
  ] as Array<[number, (r: WireReader) => unknown]>) {
    for (let len = 0; len < size; len++) {
      assert.throws(() => read(new WireReader(new Uint8Array(len))), TruncatedMessageError);
    }
  }
});

test('reader: length-delimited payload cut short is a safe truncation error', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(Uint8Array.of(1, 2, 3, 4, 5));
  const full = w.finish();
  for (let len = 0; len < full.length; len++) {
    assert.throws(() => new WireReader(full.subarray(0, len)).readLengthDelimited(), TruncatedMessageError);
  }
});

test('reader: invalid tags and wire types are rejected', () => {
  // field number 0 is illegal
  assert.throws(() => new WireReader(Uint8Array.of(0x00)).readTag(), MalformedMessageError);
  // wire type 3 (start-group) unsupported
  assert.throws(() => new WireReader(Uint8Array.of(0x0b)).readTag(), MalformedMessageError);
  // wire type 7 reserved
  assert.throws(() => new WireReader(Uint8Array.of(0x0f)).readTag(), MalformedMessageError);
});

test('reader: skipField consumes exactly one field of each wire type', () => {
  const w = new WireWriter();
  w.writeVarint(300); // varint
  w.writeFixed64(1n); // fixed64
  w.writeLengthDelimited(Uint8Array.of(9, 9, 9)); // len
  w.writeFixed32(7); // fixed32
  w.writeByte(0x42); // sentinel
  const r = new WireReader(w.finish());
  r.skipField(0);
  r.skipField(1);
  r.skipField(2);
  r.skipField(5);
  assert.equal(r.readByte(), 0x42);
  assert.ok(r.done);
});
