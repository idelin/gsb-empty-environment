import test from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireType } from '../src/reader.ts';
import { WireWriter } from '../src/writer.ts';
import { ProtobufDecodeError } from '../src/errors.ts';

function expectCode(code: string) {
  return (e: unknown) => {
    assert.ok(e instanceof ProtobufDecodeError, `expected ProtobufDecodeError, got ${String(e)}`);
    assert.equal((e as ProtobufDecodeError).code, code);
    return true;
  };
}

test('fixed32/fixed64 little-endian golden vectors', () => {
  const w = new WireWriter();
  w.writeFixed32(1).writeFixed32(0xdeadbeef).writeFixed64(1n).writeFixed64(0xffffffffffffffffn);
  const bytes = w.finish();
  assert.deepEqual(
    [...bytes],
    [1, 0, 0, 0, 0xef, 0xbe, 0xad, 0xde, 1, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff],
  );
  const r = new WireReader(bytes);
  assert.equal(r.readFixed32(), 1);
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 1n);
  assert.equal(r.readFixed64(), 0xffffffffffffffffn);
  assert.ok(r.isAtEnd);
});

test('float/double round-trip', () => {
  const w = new WireWriter();
  w.writeFloat(1.5).writeDouble(-2.25).writeFloat(NaN);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 1.5);
  assert.equal(r.readDouble(), -2.25);
  assert.ok(Number.isNaN(r.readFloat()));
});

test('truncated fixed-width reads report TRUNCATED', () => {
  assert.throws(() => new WireReader(new Uint8Array(3)).readFixed32(), expectCode('TRUNCATED'));
  assert.throws(() => new WireReader(new Uint8Array(7)).readFixed64(), expectCode('TRUNCATED'));
  assert.throws(() => new WireReader(new Uint8Array(0)).readFloat(), expectCode('TRUNCATED'));
});

test('readBytes is zero-copy and bounds-checked', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(new Uint8Array([1, 2, 3]));
  const data = w.finish();
  const r = new WireReader(data);
  const view = r.readBytes();
  assert.deepEqual([...view], [1, 2, 3]);
  assert.strictEqual(view.buffer, data.buffer, 'bytes view must share the source buffer');
  data[2] = 0xaa; // mutate source: zero-copy view observes it
  assert.equal(view[1], 0xaa);
});

test('length prefix exceeding remaining reports OUT_OF_BOUNDS', () => {
  const w = new WireWriter();
  w.writeVarint(0xffffffff).writeBytes(new Uint8Array([1, 2]));
  const r = new WireReader(w.finish());
  assert.throws(() => r.readBytes(), expectCode('OUT_OF_BOUNDS'));
});

test('huge 64-bit length prefix cannot overflow the cursor', () => {
  const w = new WireWriter();
  w.writeVarint(0xffffffffffffffffn);
  const r = new WireReader(w.finish());
  assert.throws(() => r.readBytes(), expectCode('OUT_OF_BOUNDS'));
});

test('invalid UTF-8 reports INVALID_UTF8', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(new Uint8Array([0xff, 0xfe]));
  const r = new WireReader(w.finish());
  assert.throws(() => r.readString(), expectCode('INVALID_UTF8'));
});

test('tag validation rejects field number 0 and reserved wire types', () => {
  assert.throws(() => new WireReader(new Uint8Array([0x00])).readTag(), expectCode('INVALID_TAG'));
  assert.throws(() => new WireReader(new Uint8Array([0x0e])).readTag(), expectCode('INVALID_WIRE_TYPE'));
  assert.throws(() => new WireReader(new Uint8Array([0x0f])).readTag(), expectCode('INVALID_WIRE_TYPE'));
});

test('skipField skips every wire type', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(150);
  w.writeTag(2, WireType.Fixed64).writeFixed64(9n);
  w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(new Uint8Array([1, 2, 3]));
  w.writeTag(4, WireType.Fixed32).writeFixed32(7);
  w.writeTag(5, WireType.Varint).writeVarint(1);
  const r = new WireReader(w.finish());
  for (let i = 0; i < 4; i += 1) {
    const tag = r.readTag();
    r.skipField(tag.wireType, tag.fieldNumber);
  }
  const last = r.readTag();
  assert.equal(last.fieldNumber, 5);
  assert.equal(r.readVarint64(), 1n);
  assert.ok(r.isAtEnd);
});

test('skipField skips deprecated groups, nested', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.StartGroup);
  w.writeTag(2, WireType.Varint).writeVarint(42);
  w.writeTag(3, WireType.StartGroup);
  w.writeTag(4, WireType.Fixed32).writeFixed32(1);
  w.writeTag(3, WireType.EndGroup);
  w.writeTag(1, WireType.EndGroup);
  w.writeTag(9, WireType.Varint).writeVarint(7);
  const r = new WireReader(w.finish());
  const tag = r.readTag();
  assert.equal(tag.wireType, WireType.StartGroup);
  r.skipField(tag.wireType, tag.fieldNumber);
  const after = r.readTag();
  assert.equal(after.fieldNumber, 9);
  assert.equal(r.readVarint64(), 7n);
});

test('unterminated and mismatched groups are rejected', () => {
  const unterminated = new Uint8Array([0x0b]); // tag(1, StartGroup)
  assert.throws(
    () => new WireReader(unterminated).skipField(WireType.StartGroup, 1),
    expectCode('TRUNCATED'),
  );
  const mismatched = new Uint8Array([0x0b, 0x14]); // start group 1, end group 2
  assert.throws(
    () => new WireReader(mismatched).skipField(WireType.StartGroup, 1),
    expectCode('INVALID_TAG'),
  );
});

test('reader window outside buffer reports OUT_OF_BOUNDS', () => {
  assert.throws(() => new WireReader(new Uint8Array(4), 3, 2), expectCode('OUT_OF_BOUNDS'));
});

test('fork produces a bounded zero-copy sub-reader', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(new Uint8Array([0x08, 0x01]));
  w.writeVarint(0);
  const data = w.finish();
  const r = new WireReader(data);
  const sub = r.fork();
  assert.strictEqual(sub.view.buffer, data.buffer);
  assert.equal(sub.remaining, 2);
  assert.throws(() => sub.readFixed32(), expectCode('TRUNCATED'));
});
