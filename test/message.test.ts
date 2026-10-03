import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { defineSchema } from '../src/schema.ts';
import { ProtobufDecodeError } from '../src/errors.ts';
import { addressBookSchema, nodeSchema, personSchema, sampleBook } from './helpers/fixture.ts';
import { encodeVarint } from '../src/varint.ts';

function expectCode(code: string) {
  return (e: unknown) => {
    assert.ok(e instanceof ProtobufDecodeError, `expected ProtobufDecodeError, got ${String(e)}`);
    assert.equal((e as ProtobufDecodeError).code, code);
    return true;
  };
}

test('golden bytes for a minimal Person', () => {
  const bytes = encodeMessage(personSchema, { name: 'a', id: 1 });
  // field1 (string) "a" -> 0x0A 0x01 0x61 ; field2 (int32) 1 -> 0x10 0x01
  assert.deepEqual([...bytes], [0x0a, 0x01, 0x61, 0x10, 0x01]);
  const decoded = decodeMessage(personSchema, bytes);
  assert.equal(decoded.name, 'a');
  assert.equal(decoded.id, 1);
  assert.equal(decoded.email, '', 'default applied for absent field');
  assert.deepEqual(decoded.phones, []);
  assert.deepEqual(decoded.scores, []);
});

test('multi-level nested message with packed repeated round-trips exactly', () => {
  const book = sampleBook();
  const bytes = encodeMessage(addressBookSchema, book);
  const decoded = decodeMessage(addressBookSchema, bytes);
  const expectedPeople = (book.people as Array<Record<string, unknown>>).map((p) => ({ email: '', ...p }));
  assert.deepEqual(decoded.people, expectedPeople);
  assert.equal(decoded.version, 3);
});

test('packed repeated sint32 uses length-delimited wire format', () => {
  const bytes = encodeMessage(personSchema, { name: 'x', scores: [1, -1, 300] });
  // field5, wire type 2 -> tag 0x2A, then length, then zigzag payload
  const tagIndex = bytes.indexOf(0x2a);
  assert.ok(tagIndex >= 0);
  const len = bytes[tagIndex + 1];
  const payload = bytes.subarray(tagIndex + 2, tagIndex + 2 + len);
  assert.deepEqual([...payload], [0x02, 0x01, 0xd8, 0x04]); // zigzag(1)=2, zigzag(-1)=1, zigzag(300)=600
});

test('decoder accepts unpacked encoding for a packed field (interop)', () => {
  // hand-crafted: name "x", scores as three separate varint fields
  const manual = new Uint8Array([
    0x0a, 0x01, 0x78, // name = "x"
    0x28, 0x02, // scores = 1 (zigzag 2)
    0x28, 0x01, // scores = -1
    0x28, 0xd8, 0x04, // scores = 300 (zigzag 600)
  ]);
  const decoded = decodeMessage(personSchema, manual);
  assert.deepEqual(decoded.scores, [1, -1, 300]);
});

test('empty message decodes to defaults only', () => {
  const decoded = decodeMessage(addressBookSchema, new Uint8Array(0));
  assert.deepEqual(decoded, { people: [], version: 1 });
});

test('missing required field reports MISSING_REQUIRED on decode', () => {
  // Person without the required "name": only id present -> 0x10 0x01
  assert.throws(
    () => decodeMessage(personSchema, new Uint8Array([0x10, 0x01])),
    expectCode('MISSING_REQUIRED'),
  );
});

test('missing required field reports MISSING_REQUIRED on encode', () => {
  assert.throws(() => encodeMessage(personSchema, { id: 1 }), expectCode('MISSING_REQUIRED'));
});

test('wire type mismatch against schema reports SCHEMA_MISMATCH', () => {
  // field 2 (id, varint expected) encoded as fixed32: tag = (2<<3)|5 = 0x15
  const bad = new Uint8Array([0x0a, 0x01, 0x61, 0x15, 0, 0, 0, 0]);
  assert.throws(() => decodeMessage(personSchema, bad), expectCode('SCHEMA_MISMATCH'));
});

test('recursive schema round-trips and enforces recursion limit', () => {
  let node: Record<string, unknown> = { value: 5n };
  for (let i = 0; i < 10; i += 1) node = { child: node, value: BigInt(i) };
  const bytes = encodeMessage(nodeSchema, node);
  const decoded = decodeMessage(nodeSchema, bytes) as Record<string, unknown>;
  let depth = 0;
  let cursor = decoded;
  while (cursor.child) {
    depth += 1;
    cursor = cursor.child as Record<string, unknown>;
  }
  assert.equal(depth, 10);
  assert.equal(cursor.value, 5n);

  let deep: Record<string, unknown> = {};
  for (let i = 0; i < 200; i += 1) deep = { child: deep };
  assert.throws(() => encodeMessage(nodeSchema, deep), expectCode('RECURSION_LIMIT'));
});

test('hand-crafted deeply nested bytes hit RECURSION_LIMIT on decode', () => {
  // field 1 (message), nested 100 levels deep, innermost is empty
  let payload = new Uint8Array(0);
  for (let i = 0; i < 100; i += 1) {
    const len = encodeVarint(payload.length);
    const next = new Uint8Array(1 + len.length + payload.length);
    next[0] = 0x0a; // tag(1, LEN)
    next.set(len, 1);
    next.set(payload, 1 + len.length);
    payload = next;
  }
  assert.throws(() => decodeMessage(nodeSchema, payload), expectCode('RECURSION_LIMIT'));
  // with a raised limit the same buffer decodes fine
  const decoded = decodeMessage(nodeSchema, payload, { maxDepth: 200 });
  assert.ok(decoded.child);
});

test('schema validation rejects illegal descriptors', () => {
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 1, kind: 'int32' }, { name: 'b', number: 1, kind: 'int32' }]),
    expectCode('SCHEMA_MISMATCH'),
  );
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 0, kind: 'int32' }]),
    expectCode('SCHEMA_MISMATCH'),
  );
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 19000, kind: 'int32' }]),
    expectCode('SCHEMA_MISMATCH'),
  );
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 1, kind: 'string', repeated: true, packed: true }]),
    expectCode('SCHEMA_MISMATCH'),
  );
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 1, kind: 'message' }]),
    expectCode('SCHEMA_MISMATCH'),
  );
  assert.throws(
    () => defineSchema('Bad', [{ name: 'a', number: 1, kind: 'int32', packed: true }]),
    expectCode('SCHEMA_MISMATCH'),
  );
});

test('zero-copy: decoded bytes fields are views into the input buffer', () => {
  const bytes = encodeMessage(addressBookSchema, sampleBook());
  const decoded = decodeMessage(addressBookSchema, bytes);
  const people = decoded.people as Array<Record<string, unknown>>;
  const avatar = people[0].avatar as Uint8Array;
  assert.strictEqual(avatar.buffer, bytes.buffer);
});
