import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DecodeError,
  TruncatedError,
  WireReader,
  WireType,
  WireWriter,
} from '../src/wire.ts';

function roundtripVarint(values: (number | bigint)[]): void {
  const writer = new WireWriter();
  for (const v of values) writer.writeVarint(v);
  const reader = new WireReader().reset(writer.finish());
  for (const v of values) {
    if (typeof v === 'bigint' || v > Number.MAX_SAFE_INTEGER) {
      assert.equal(reader.readVarint64(), BigInt(v));
    } else {
      assert.equal(reader.readVarint(), v);
    }
  }
  assert.ok(reader.eof());
}

test('varint: 7-bit boundary values round-trip', () => {
  roundtripVarint([0, 1, 127, 128, 129, 300, 16383, 16384, 2 ** 21 - 1, 2 ** 21]);
});

test('varint: multi-byte boundaries up to 2^53-1', () => {
  roundtripVarint([2 ** 28 - 1, 2 ** 28, 2 ** 31 - 1, 2 ** 31, 2 ** 32 - 1, 2 ** 32, 2 ** 53 - 1]);
});

test('varint64: full 64-bit range including max uint64', () => {
  roundtripVarint([2n ** 63n, 2n ** 64n - 1n, 0xffff_ffff_ffff_ffffn]);
});

test('varint: canonical encodings match protobuf spec', () => {
  const writer = new WireWriter();
  writer.writeVarint(1);
  assert.deepEqual([...writer.finish()], [0x01]);
  writer.reset();
  writer.writeVarint(300);
  assert.deepEqual([...writer.finish()], [0xac, 0x02]);
  writer.reset();
  writer.writeVarint(127);
  assert.deepEqual([...writer.finish()], [0x7f]);
  writer.reset();
  writer.writeVarint(128);
  assert.deepEqual([...writer.finish()], [0x80, 0x01]);
});

test('varint: truncated input raises TruncatedError', () => {
  const reader = new WireReader().reset(new Uint8Array([0x80]));
  assert.throws(() => reader.readVarint(), TruncatedError);
  const reader2 = new WireReader().reset(new Uint8Array([0xac]));
  assert.throws(() => reader2.readVarint(), TruncatedError);
});

test('varint: more than 10 bytes is malformed', () => {
  const reader = new WireReader().reset(new Uint8Array(11).fill(0x80));
  assert.throws(() => reader.readVarint(), DecodeError);
  const reader64 = new WireReader().reset(new Uint8Array(11).fill(0x80));
  assert.throws(() => reader64.readVarint64(), DecodeError);
});

test('varint: value beyond 2^53-1 rejected for number reads', () => {
  const writer = new WireWriter();
  writer.writeVarint(2n ** 60n);
  const reader = new WireReader().reset(writer.finish());
  assert.throws(() => reader.readVarint(), DecodeError);
});

test('zigzag: spec vectors for 32-bit', () => {
  const cases: [number, number][] = [
    [0, 0],
    [-1, 1],
    [1, 2],
    [-2, 3],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  const writer = new WireWriter();
  for (const [n] of cases) writer.writeZigZag32(n);
  const reader = new WireReader().reset(writer.finish());
  for (const [n, encoded] of cases) {
    assert.equal(reader.readVarint(), encoded, `encoding of ${n}`);
  }
  const reader2 = new WireReader().reset(writer.finish());
  for (const [n] of cases) {
    assert.equal(reader2.readZigZag32(), n);
  }
});

test('zigzag: 64-bit extremes', () => {
  const cases: bigint[] = [0n, -1n, 1n, 2n ** 63n - 1n, -(2n ** 63n)];
  const writer = new WireWriter();
  for (const n of cases) writer.writeZigZag64(n);
  const bytes = writer.finish();
  const reader = new WireReader().reset(bytes);
  for (const n of cases) assert.equal(reader.readZigZag64(), n);
  assert.ok(reader.eof());
  // -(2^63) must encode to 2^64-1
  const tail = new WireWriter();
  tail.writeZigZag64(-(2n ** 63n));
  const tailReader = new WireReader().reset(tail.finish());
  assert.equal(tailReader.readVarint64(), 2n ** 64n - 1n);
});

test('fixed32/fixed64: round-trip and little-endian layout', () => {
  const writer = new WireWriter();
  writer.writeFixed32(0xdeadbeef);
  writer.writeFixed64(0x0123_4567_89ab_cdefn);
  const bytes = writer.finish();
  assert.deepEqual([...bytes.subarray(0, 4)], [0xef, 0xbe, 0xad, 0xde]);
  const reader = new WireReader().reset(bytes);
  assert.equal(reader.readFixed32(), 0xdeadbeef);
  assert.equal(reader.readFixed64(), 0x0123_4567_89ab_cdefn);
  assert.ok(reader.eof());
});

test('fixed32/fixed64: truncation raises TruncatedError', () => {
  assert.throws(() => new WireReader().reset(new Uint8Array(3)).readFixed32(), TruncatedError);
  assert.throws(() => new WireReader().reset(new Uint8Array(7)).readFixed64(), TruncatedError);
  assert.throws(() => new WireReader().reset(new Uint8Array(0)).readFixed32(), TruncatedError);
});

test('length-delimited: bytes and strings round-trip', () => {
  const writer = new WireWriter();
  writer.writeString('héllo, 世界');
  writer.writeBytes(new Uint8Array([1, 2, 3, 255]));
  const reader = new WireReader().reset(writer.finish());
  assert.equal(reader.readString(), 'héllo, 世界');
  assert.deepEqual([...reader.readBytes()], [1, 2, 3, 255]);
  assert.ok(reader.eof());
});

test('length-delimited: hostile length prefix is rejected safely', () => {
  const writer = new WireWriter();
  writer.writeVarint(0xffff_ffff);
  const reader = new WireReader().reset(writer.finish());
  assert.throws(() => reader.readBytes(), TruncatedError);
});

test('tag: field/wire decoding and invalid tags', () => {
  const writer = new WireWriter();
  writer.writeTag(15, WireType.Fixed32);
  const reader = new WireReader().reset(writer.finish());
  const tag = reader.readTag();
  assert.equal(Math.floor(tag / 8), 15);
  assert.equal(tag & 7, WireType.Fixed32);

  const badWire = new WireReader().reset(new Uint8Array([0x0b])); // field 1, wire 3 (group)
  assert.throws(() => badWire.readTag(), DecodeError);
  const badField = new WireReader().reset(new Uint8Array([0x00])); // field 0
  assert.throws(() => badField.readTag(), DecodeError);
});

test('nested limits: push/pop without copying bytes', () => {
  // outer: field 1 (len-delimited) -> inner: field 1 varint 150, field 2 varint 1
  const inner = new WireWriter();
  inner.writeTag(1, WireType.Varint).writeVarint(150);
  inner.writeTag(2, WireType.Varint).writeVarint(1);
  const outer = new WireWriter();
  outer.writeTag(1, WireType.LengthDelimited).writeBytes(inner.finish());
  outer.writeTag(9, WireType.Varint).writeVarint(7);

  const reader = new WireReader().reset(outer.finish());
  assert.equal(reader.readTag(), 1 * 8 + 2);
  reader.pushLimit(reader.readLength());
  assert.equal(reader.readTag(), 1 * 8 + 0);
  assert.equal(reader.readVarint(), 150);
  assert.equal(reader.readTag(), 2 * 8 + 0);
  assert.equal(reader.readVarint(), 1);
  reader.popLimit();
  assert.equal(reader.readTag(), 9 * 8 + 0);
  assert.equal(reader.readVarint(), 7);
  assert.ok(reader.eof());
});

test('nested limits: overrun inside nested message raises TruncatedError', () => {
  const outer = new WireWriter();
  outer.writeTag(1, WireType.LengthDelimited).writeBytes(new Uint8Array([0x08, 0x96])); // varint 150 truncated
  const reader = new WireReader().reset(outer.finish());
  reader.readTag();
  reader.pushLimit(reader.readLength());
  reader.readTag();
  assert.throws(() => reader.readVarint(), TruncatedError);
});

test('nested limits: popLimit before full consumption fails', () => {
  const outer = new WireWriter();
  outer.writeTag(1, WireType.LengthDelimited).writeBytes(new Uint8Array([0x08, 0x01]));
  const reader = new WireReader().reset(outer.finish());
  reader.readTag();
  reader.pushLimit(reader.readLength());
  assert.throws(() => reader.popLimit(), DecodeError);
});

test('skipField: every wire type', () => {
  const writer = new WireWriter();
  writer.writeTag(1, WireType.Varint).writeVarint(123);
  writer.writeTag(2, WireType.Fixed64).writeFixed64(42n);
  writer.writeTag(3, WireType.LengthDelimited).writeString('skip me');
  writer.writeTag(4, WireType.Fixed32).writeFixed32(7);
  writer.writeTag(5, WireType.Varint).writeVarint(999);
  const reader = new WireReader().reset(writer.finish());
  for (let i = 0; i < 4; i++) {
    const tag = reader.readTag();
    reader.skipField(tag & 7);
  }
  assert.equal(reader.readTag(), 5 * 8 + 0);
  assert.equal(reader.readVarint(), 999);
  assert.ok(reader.eof());
});

test('skipField: truncated skip raises TruncatedError', () => {
  const reader = new WireReader().reset(new Uint8Array([0x09, 0x01])); // fixed64 tag, 1 byte
  const tag = reader.readTag();
  assert.throws(() => reader.skipField(tag & 7), TruncatedError);
});

test('reader reset: invalid range rejected', () => {
  const reader = new WireReader();
  assert.throws(() => reader.reset(new Uint8Array(4), 2, 1), DecodeError);
  assert.throws(() => reader.reset(new Uint8Array(4), 0, 5), DecodeError);
});
