import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader, WireWriter, WireError } from '../src/index.ts';

test('fixed32/fixed64 little-endian round-trip', () => {
  const w = new WireWriter();
  w.writeFixed32(0xdeadbeef).writeFixed64(0x0123456789abcdefn);
  const r = new WireReader(w.finish());
  assert.equal(r.readFixed32(), 0xdeadbeef);
  assert.equal(r.readFixed64(), 0x0123456789abcdefn);
  assert.equal(r.remaining, 0);
});

test('float/double round-trip', () => {
  const w = new WireWriter();
  w.writeFloat(3.5).writeDouble(-1.25e300);
  const r = new WireReader(w.finish());
  assert.equal(r.readFloat(), 3.5);
  assert.equal(r.readDouble(), -1.25e300);
});

test('readTag parses field and wire type', () => {
  const w = new WireWriter();
  w.writeTag(15, 2);
  const r = new WireReader(w.finish());
  assert.deepEqual(r.readTag(), { field: 15, wire: 2 });
  assert.equal(r.readTag(), null);
});

test('readTag rejects field 0 and wire types 6/7', () => {
  const r0 = new WireReader(new Uint8Array([0x00]));
  assert.throws(() => r0.readTag(), (e: unknown) => (e as WireError).code === 'INVALID_TAG');
  const r6 = new WireReader(new Uint8Array([0x0e]));
  assert.throws(() => r6.readTag(), (e: unknown) => (e as WireError).code === 'INVALID_WIRE_TYPE');
  const r7 = new WireReader(new Uint8Array([0x0f]));
  assert.throws(() => r7.readTag(), (e: unknown) => (e as WireError).code === 'INVALID_WIRE_TYPE');
});

test('reads past end raise TRUNCATED with offset', () => {
  const r = new WireReader(new Uint8Array([1, 2]));
  assert.throws(() => r.readFixed32(), (e: unknown) => {
    assert.equal((e as WireError).code, 'TRUNCATED');
    assert.equal((e as WireError).offset, 0);
    return true;
  });
  const r2 = new WireReader(new Uint8Array([1, 2, 3, 4, 5]));
  assert.throws(() => r2.readFixed64(), (e: unknown) => (e as WireError).code === 'TRUNCATED');
});

test('length-delimited overrun is a safe bounds error', () => {
  const r = new WireReader(new Uint8Array([0x05, 0x01, 0x02]));
  assert.throws(() => r.readBytes(), (e: unknown) => {
    assert.equal((e as WireError).code, 'TRUNCATED');
    assert.match((e as WireError).message, /declares 5 byte\(s\) but only 2 remain/);
    return true;
  });
});

test('subreader is limited to declared length', () => {
  const w = new WireWriter();
  const inner = new WireWriter();
  inner.writeVarint(42);
  w.writeLengthDelimited(inner.finish());
  w.writeVarint(7);
  const r = new WireReader(w.finish());
  const sub = r.readSubreader();
  assert.equal(sub.readVarint(), 42n);
  assert.ok(sub.done);
  assert.equal(r.readVarint(), 7n);
});

test('invalid UTF-8 string rejected', () => {
  const w = new WireWriter();
  w.writeLengthDelimited(new Uint8Array([0xff, 0xfe]));
  const r = new WireReader(w.finish());
  assert.throws(() => r.readString(), (e: unknown) => (e as WireError).code === 'INVALID_UTF8');
});

test('skip handles every wire type and nested groups', () => {
  const w = new WireWriter();
  w.writeTag(1, 0); w.writeVarint(999);                                // varint
  w.writeTag(2, 1); w.writeFixed64(1n);                                // i64
  w.writeTag(3, 2); w.writeLengthDelimited(new Uint8Array([1, 2, 3])); // len
  w.writeTag(4, 3);                                                    // sgroup
  w.writeTag(1, 0); w.writeVarint(5);                                  //   nested field
  w.writeTag(4, 4);                                                    // egroup
  w.writeTag(5, 5); w.writeFixed32(7);                                 // i32
  w.writeTag(6, 0); w.writeVarint(12345);                              // trailing value
  const r = new WireReader(w.finish());
  for (let i = 0; i < 5; i++) {
    const tag = r.readTag();
    assert.ok(tag);
    r.skip(tag.wire);
  }
  const last = r.readTag();
  assert.deepEqual(last, { field: 6, wire: 0 });
  assert.equal(r.readVarint(), 12345n);
  assert.ok(r.done);
});

test('skip of truncated fields raises TRUNCATED', () => {
  const r = new WireReader(new Uint8Array([0x0a, 0xff]));
  assert.throws(() => r.skip(2), (e: unknown) => (e as WireError).code === 'TRUNCATED');
  const r2 = new WireReader(new Uint8Array([0x01, 0x02]));
  assert.throws(() => r2.skip(1), (e: unknown) => (e as WireError).code === 'TRUNCATED');
  const r3 = new WireReader(new Uint8Array([0x01, 0x02, 0x03]));
  assert.throws(() => r3.skip(5), (e: unknown) => (e as WireError).code === 'TRUNCATED');
});

test('unterminated group raises TRUNCATED', () => {
  const w = new WireWriter();
  w.writeTag(1, 3);
  w.writeTag(2, 0);
  w.writeVarint(1);
  const r = new WireReader(w.finish());
  assert.throws(() => r.skip(3), (e: unknown) => (e as WireError).code === 'TRUNCATED');
});

test('reader window validation', () => {
  assert.throws(() => new WireReader(new Uint8Array(2), 1, 5), (e: unknown) => (e as WireError).code === 'TRUNCATED');
});
