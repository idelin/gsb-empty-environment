import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { defineSchema } from '../src/schema.ts';
import { WireWriter } from '../src/writer.ts';
import { WireType } from '../src/reader.ts';

// v1 schema: the "old" reader
const userV1 = defineSchema('User', [
  { name: 'name', number: 1, kind: 'string' },
  { name: 'id', number: 2, kind: 'int32' },
]);

// v2 schema: adds fields 3..6 of every wire type (forward-compat source)
const userV2 = defineSchema('User', [
  { name: 'name', number: 1, kind: 'string' },
  { name: 'id', number: 2, kind: 'int32' },
  { name: 'email', number: 3, kind: 'string', defaultValue: 'n/a' },
  { name: 'tags', number: 4, kind: 'uint32', repeated: true, packed: true },
  { name: 'score', number: 5, kind: 'double', defaultValue: 0 },
  { name: 'flags', number: 6, kind: 'fixed32' },
]);

test('forward compatibility: old reader skips unknown fields of every wire type', () => {
  const bytes = encodeMessage(userV2, {
    name: 'neo',
    id: 7,
    email: 'neo@zion',
    tags: [1, 2, 3],
    score: 9.5,
    flags: 0xcafebabe,
  });
  const decoded = decodeMessage(userV1, bytes);
  assert.deepEqual(decoded, { name: 'neo', id: 7 });
});

test('backward compatibility: new reader fills defaults for missing fields', () => {
  const bytes = encodeMessage(userV1, { name: 'trinity', id: 8 });
  const decoded = decodeMessage(userV2, bytes);
  assert.deepEqual(decoded, {
    name: 'trinity',
    id: 8,
    email: 'n/a',
    tags: [],
    score: 0,
  });
  assert.equal(decoded.flags, undefined);
});

test('unknown deprecated group fields are skipped', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(new TextEncoder().encode('morpheus'));
  w.writeTag(15, WireType.StartGroup);
  w.writeTag(1, WireType.Varint).writeVarint(99);
  w.writeTag(15, WireType.EndGroup);
  w.writeTag(2, WireType.Varint).writeVarint(3);
  const decoded = decodeMessage(userV1, w.finish());
  assert.deepEqual(decoded, { name: 'morpheus', id: 3 });
});

test('field reordered on the wire still decodes (order independence)', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Varint).writeVarint(11);
  w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(new TextEncoder().encode('switch'));
  const decoded = decodeMessage(userV1, w.finish());
  assert.deepEqual(decoded, { name: 'switch', id: 11 });
});

test('duplicate singular field on the wire: last one wins (protobuf semantics)', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Varint).writeVarint(1);
  w.writeTag(2, WireType.Varint).writeVarint(2);
  const decoded = decodeMessage(userV1, w.finish());
  assert.equal(decoded.id, 2);
});
