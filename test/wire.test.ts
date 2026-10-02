import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireType, WireWriter } from '../src/wire.ts';
import { DecodeError, EncodeError } from '../src/errors.ts';

test('tag encode/decode roundtrip', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeTag(15, WireType.LengthDelimited).writeTag(2047, WireType.Fixed64);
  const r = new WireReader(w.finish());
  assert.deepEqual(r.readTag(), { fieldNumber: 1, wireType: WireType.Varint });
  assert.deepEqual(r.readTag(), { fieldNumber: 15, wireType: WireType.LengthDelimited });
  assert.deepEqual(r.readTag(), { fieldNumber: 2047, wireType: WireType.Fixed64 });
  assert.ok(r.eof);
});

test('tag rejects field number 0 and groups', () => {
  assert.throws(() => new WireReader(new Uint8Array([0x00])).readTag(), /field number 0/);
  assert.throws(() => new WireReader(new Uint8Array([0x0b])).readTag(), /groups/); // field 1, SGROUP
});

test('fixed32/fixed64/float/double roundtrip little-endian', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef).writeSFixed32(-12345).writeFloat(1.5)
    .writeFixed64(0x0102030405060708n).writeSFixed64(-42n).writeDouble(Math.PI);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readSFixed32(), -12345);
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readFixed64(), 0x0102030405060708n);
  assert.equal(r.readSFixed64(), -42n);
  assert.equal(r.readDouble(), Math.PI);
  assert.ok(r.eof);
});

test('fixed64 is little-endian on the wire', () => {
  const w = new WireWriter();
  w.writeFixed64(0x0807060504030201n);
  assert.deepEqual([...w.finish()], [1, 2, 3, 4, 5, 6, 7, 8]);
});

test('truncated fixed32/fixed64 throw DecodeError, never read OOB', () => {
  assert.throws(() => new WireReader(new Uint8Array([1, 2, 3])).readFixed32(),
    (e) => e instanceof DecodeError && /truncated/.test(e.message));
  assert.throws(() => new WireReader(new Uint8Array([1, 2, 3, 4, 5])).readFixed64(), DecodeError);
  assert.throws(() => new WireReader(new Uint8Array([])).readDouble(), DecodeError);
});

test('length-delimited overrun is caught by bounds check', () => {
  const w = new WireWriter();
  w.writeVarint(100); // declares 100 bytes
  w.writeRaw(new Uint8Array([1, 2, 3])); // but only 3 follow
  const r = new WireReader(w.finish());
  assert.throws(() => r.readBytes(),
    (e) => e instanceof DecodeError && /exceeds remaining/.test(e.message));
});

test('readBytes of exact length succeeds at buffer end', () => {
  const w = new WireWriter();
  w.writeBytes(new Uint8Array([9, 8, 7]));
  const r = new WireReader(w.finish());
  assert.deepEqual([...r.readBytes()], [9, 8, 7]);
  assert.ok(r.eof);
});

test('skip handles every wire type and rejects unknown types', () => {
  const w = new WireWriter();
  w.writeVarint(7).writeFixed64(1n).writeBytes(new Uint8Array([1])).writeFixed32(2);
  const r = new WireReader(w.finish());
  r.skip(WireType.Varint);
  r.skip(WireType.Fixed64);
  r.skip(WireType.LengthDelimited);
  r.skip(WireType.Fixed32);
  assert.ok(r.eof);
  assert.throws(() => new WireReader(new Uint8Array([0])).skip(6 as WireType), DecodeError);
});

test('sub-reader is bounded to its declared window', () => {
  const w = new WireWriter();
  const inner = new WireWriter();
  inner.writeRaw(new Uint8Array([0x80])); // unterminated varint inside the sub-message
  w.writeBytes(inner.finish());
  w.writeVarint(0x01); // byte belonging to the outer message
  const r = new WireReader(w.finish());
  const sub = r.readSubReader();
  // The sub-reader must NOT consume the outer byte to complete the varint.
  assert.throws(() => sub.readVarint(), /truncated/);
  assert.equal(r.readVarint(), 1n); // outer stream unaffected
});

test('writer rejects invalid field numbers', () => {
  assert.throws(() => new WireWriter().writeTag(0, WireType.Varint), EncodeError);
  assert.throws(() => new WireWriter().writeTag(2 ** 30, WireType.Varint), EncodeError);
});
