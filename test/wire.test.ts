import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MalformedTagError, WireReader, WireType, WireWriter } from '../src/wire.ts';
import { TruncatedFieldError } from '../src/varint.ts';

test('writer/reader round-trip across all wire types', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(150n);
  w.writeTag(2, WireType.Fixed64).writeFixed64(0xdeadbeefcafen);
  w.writeTag(3, WireType.LengthDelimited).writeString('hello');
  w.writeTag(4, WireType.Fixed32).writeFixed32(42);
  const buf = w.finish();

  const r = new WireReader(buf);
  assert.deepEqual(r.readTag(), { fieldNumber: 1, wireType: WireType.Varint });
  assert.equal(r.readVarint(), 150n);
  assert.deepEqual(r.readTag(), { fieldNumber: 2, wireType: WireType.Fixed64 });
  assert.equal(r.readFixed64(), 0xdeadbeefcafen);
  assert.deepEqual(r.readTag(), { fieldNumber: 3, wireType: WireType.LengthDelimited });
  assert.equal(r.readString(), 'hello');
  assert.deepEqual(r.readTag(), { fieldNumber: 4, wireType: WireType.Fixed32 });
  assert.equal(r.readFixed32(), 42);
  assert.ok(r.done);
  assert.equal(r.remaining, 0);
});

test('reader skips unknown fields of every wire type', () => {
  const w = new WireWriter();
  w.writeTag(10, WireType.Varint).writeVarint(1n);
  w.writeTag(11, WireType.Fixed64).writeFixed64(2n);
  w.writeTag(12, WireType.LengthDelimited).writeString('skip me');
  w.writeTag(13, WireType.Fixed32).writeFixed32(3);
  w.writeTag(1, WireType.Varint).writeVarint(99n);

  const r = new WireReader(w.finish());
  for (const wt of [WireType.Varint, WireType.Fixed64, WireType.LengthDelimited, WireType.Fixed32]) {
    const tag = r.readTag();
    assert.equal(tag.wireType, wt);
    r.skip(tag.wireType);
  }
  assert.deepEqual(r.readTag(), { fieldNumber: 1, wireType: WireType.Varint });
  assert.equal(r.readVarint(), 99n);
});

test('truncation at every byte boundary raises TruncatedFieldError', () => {
  const w = new WireWriter();
  // a single length-delimited field: every proper non-empty prefix is truncated
  w.writeTag(1, WireType.LengthDelimited).writeString('nested payload');
  const full = w.finish();

  // sanity: the full buffer parses cleanly
  const ok = new WireReader(full);
  while (!ok.done) {
    const t = ok.readTag();
    ok.skip(t.wireType);
  }

  // every non-empty strict prefix must fail safely, never read out of bounds
  for (let len = 1; len < full.length; len++) {
    const truncated = full.subarray(0, len);
    assert.throws(
      () => {
        const r = new WireReader(truncated);
        while (!r.done) {
          const t = r.readTag();
          r.skip(t.wireType);
        }
      },
      TruncatedFieldError,
      `prefix of length ${len} should fail safely`,
    );
  }
});

test('length-delimited field with declared length beyond buffer is rejected', () => {
  // tag: field 1, wire type 2; length: 100; payload: only 2 bytes present
  const buf = new Uint8Array([0x0a, 0x64, 0x01, 0x02]);
  const r = new WireReader(buf);
  r.readTag();
  assert.throws(() => r.readBytes(), TruncatedFieldError);
});

test('malformed tags are rejected', () => {
  // field number 0 is illegal
  assert.throws(() => new WireReader(new Uint8Array([0x00])).readTag(), MalformedTagError);
  // wire types 3, 4, 6, 7 are not supported (groups deprecated / invalid)
  for (const wt of [3, 4, 6, 7]) {
    const tagByte = (1 << 3) | wt;
    assert.throws(() => new WireReader(new Uint8Array([tagByte])).readTag(), MalformedTagError);
  }
});

test('empty buffer reader is immediately done', () => {
  const r = new WireReader(new Uint8Array(0));
  assert.ok(r.done);
  assert.throws(() => r.readTag(), TruncatedFieldError);
});

test('sub-message window cannot exceed parent buffer', () => {
  const buf = new Uint8Array(4);
  assert.throws(() => new WireReader(buf, 10), TruncatedFieldError);
});
