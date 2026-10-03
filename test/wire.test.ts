import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader } from '../src/wire/reader.ts';
import { WireWriter } from '../src/wire/writer.ts';
import { WireType } from '../src/wire/types.ts';
import { DecodeError, TruncatedFieldError } from '../src/wire/errors.ts';

test('fixed32 little-endian roundtrip', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef);
  assert.deepEqual([...w.finish()], [0xef, 0xbe, 0xad, 0xde]);
  const r = WireReader.of(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.ok(r.done);
});

test('fixed64 little-endian roundtrip', () => {
  const w = new WireWriter();
  w.writeFixed64(0x0123456789abcdefn);
  assert.deepEqual([...w.finish()], [0xef, 0xcd, 0xab, 0x89, 0x67, 0x45, 0x23, 0x01]);
  const r = WireReader.of(w.finish());
  assert.equal(r.readFixed64(), 0x0123456789abcdefn);
});

test('float and double roundtrip', () => {
  const w = new WireWriter();
  w.writeFloat(1.5).writeDouble(-273.15);
  const r = WireReader.of(w.finish());
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readDouble(), -273.15);
});

test('tag roundtrip and validation', () => {
  const w = new WireWriter();
  w.writeTag(15, WireType.LengthDelimited);
  const r = WireReader.of(w.finish());
  const tag = r.readTag();
  assert.equal(tag.fieldNumber, 15);
  assert.equal(tag.wireType, WireType.LengthDelimited);
  assert.throws(() => WireReader.of(new Uint8Array([0x00])).readTag(), DecodeError);
  assert.throws(() => WireReader.of(new Uint8Array([(1 << 3) | 3])).readTag(), DecodeError);
});

test('skipField covers every wire type', () => {
  const w = new WireWriter();
  w.writeVarint(300n);
  w.writeFixed64(1n);
  w.writeLengthDelimited(new Uint8Array([9, 9, 9]));
  w.writeFixed32(7);
  w.writeVarint(0x55n);
  const r = WireReader.of(w.finish());
  r.skipField(WireType.Varint);
  r.skipField(WireType.Fixed64);
  r.skipField(WireType.LengthDelimited);
  r.skipField(WireType.Fixed32);
  assert.equal(r.readVarint(), 0x55n);
  assert.ok(r.done);
});

test('bounds guard: fixed32 with insufficient bytes', () => {
  const r = WireReader.of(new Uint8Array([1, 2, 3]));
  try {
    r.readFixed32();
    assert.unreachable();
  } catch (err) {
    assert.ok(err instanceof TruncatedFieldError);
    assert.equal(err.offset, 0);
    assert.equal(err.needed, 4);
    assert.equal(err.available, 3);
  }
});

test('bounds guard: fixed64 with insufficient bytes', () => {
  const r = WireReader.of(new Uint8Array([1, 2, 3, 4]));
  assert.throws(() => r.readFixed64(), TruncatedFieldError);
});

test('bounds guard: length-delimited payload longer than remaining', () => {
  const buf = new Uint8Array([0x05, 0x61, 0x62]);
  const r = WireReader.of(buf);
  try {
    r.readLengthDelimited();
    assert.unreachable();
  } catch (err) {
    assert.ok(err instanceof TruncatedFieldError);
    assert.equal(err.needed, 5);
    assert.equal(err.available, 2);
  }
});

test('bounds guard: readBytes negative and overflow', () => {
  const r = WireReader.of(new Uint8Array([1, 2]));
  assert.throws(() => r.readBytes(-1), DecodeError);
  assert.throws(() => r.readBytes(3), TruncatedFieldError);
});

test('subreader is confined to its payload window', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(new Uint8Array([0x08, 0x01]));
  w.writeVarint(0x7fn);
  const r = WireReader.of(w.finish());
  const sub = r.readSubreader();
  assert.equal(sub.remaining, 2);
  const tag = sub.readTag();
  assert.equal(tag.fieldNumber, 1);
  assert.equal(sub.readVarint(), 1n);
  assert.ok(sub.done);
  assert.equal(r.readVarint(), 0x7fn);
});

test('reader window constructor validates range', () => {
  assert.throws(() => new WireReader(new Uint8Array(2), 0, 5), TruncatedFieldError);
});
