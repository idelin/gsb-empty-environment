import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/codec.ts';
import type { FieldDescriptor, MessageDescriptor } from '../src/schema.ts';
import { DecodeError, TruncatedFieldError } from '../src/wire/errors.ts';
import { Address, Person, samplePerson } from './person.ts';

test('encode then decode restores nested structure with packed arrays', () => {
  const bytes = encodeMessage(Person, samplePerson);
  const decoded = decodeMessage(Person, bytes);
  assert.deepEqual(decoded, samplePerson);
});

test('hand-crafted byte stream decodes to expected structure', () => {
  const bytes = new Uint8Array([
    0x08, 0x01, // field 1 (id): varint 1
    0x12, 0x02, 0x61, 0x62, // field 2 (name): "ab"
    0x22, 0x02, 0x02, 0x01, // field 4 (scores, packed sint32): [1, -1]
    0x2a, 0x09, // field 5 (address): 9-byte nested message
    0x0a, 0x02, 0x68, 0x7a, //   city = "hz"
    0x15, 0xf0, 0xba, 0x04, 0x00, //   zip = 310000 (fixed32 LE)
    0x32, 0x01, 0x78, // field 6 (tags): "x"
    0x32, 0x01, 0x79, // field 6 (tags): "y"
    0x3a, 0x02, 0x01, 0x00, // field 7 (lucky, packed bool): [true, false]
  ]);
  const decoded = decodeMessage(Person, bytes);
  assert.deepEqual(decoded, {
    id: 1n,
    name: 'ab',
    scores: [1, -1],
    address: { city: 'hz', zip: 310000 },
    tags: ['x', 'y'],
    lucky: [true, false],
  });
});

test('encoder emits canonical hand-verifiable bytes', () => {
  const bytes = encodeMessage(Person, {
    id: 1n,
    name: 'ab',
    scores: [1, -1],
    address: { city: 'hz', zip: 310000 },
    tags: ['x', 'y'],
    lucky: [true, false],
  });
  assert.deepEqual(
    [...bytes],
    [
      0x08, 0x01,
      0x12, 0x02, 0x61, 0x62,
      0x22, 0x02, 0x02, 0x01,
      0x2a, 0x09, 0x0a, 0x02, 0x68, 0x7a, 0x15, 0xf0, 0xba, 0x04, 0x00,
      0x32, 0x01, 0x78,
      0x32, 0x01, 0x79,
      0x3a, 0x02, 0x01, 0x00,
    ],
  );
});

test('decoder accepts both packed and unpacked encodings of repeated scalars', () => {
  const packed = new Uint8Array([0x22, 0x02, 0x02, 0x01]);
  const unpacked = new Uint8Array([0x20, 0x02, 0x20, 0x01]);
  assert.deepEqual(decodeMessage(Person, packed).scores, [1, -1]);
  assert.deepEqual(decodeMessage(Person, unpacked).scores, [1, -1]);
});

test('truncation inside nested message raises TruncatedFieldError', () => {
  const full = encodeMessage(Person, samplePerson);
  for (const cut of [1, 3, 7, Math.floor(full.length / 2), full.length - 1]) {
    assert.throws(
      () => decodeMessage(Person, full.subarray(0, cut)),
      TruncatedFieldError,
      `cut at ${cut}`,
    );
  }
});

test('truncation inside packed payload raises TruncatedFieldError', () => {
  const bytes = new Uint8Array([0x22, 0x05, 0x02, 0x01]);
  assert.throws(() => decodeMessage(Person, bytes), TruncatedFieldError);
});

test('truncated varint tag at end of buffer', () => {
  const bytes = new Uint8Array([0x08, 0x01, 0x80]);
  assert.throws(() => decodeMessage(Person, bytes), TruncatedFieldError);
});

test('unknown fields are skipped by default and rejected when disallowed', () => {
  const bytes = new Uint8Array([
    0x08, 0x01, // id = 1
    0xf8, 0x06, 0x2a, // field 111 varint 42 (unknown)
    0x12, 0x01, 0x61, // name = "a"
  ]);
  const decoded = decodeMessage(Person, bytes);
  assert.equal(decoded.id, 1n);
  assert.equal(decoded.name, 'a');
  assert.throws(() => decodeMessage(Person, bytes, { allowUnknownFields: false }), DecodeError);
});

test('wire type mismatch on known field raises DecodeError', () => {
  const bytes = new Uint8Array([0x10, 0x01]); // field 2 (name, string) as varint
  assert.throws(() => decodeMessage(Person, bytes), DecodeError);
});

test('nesting depth limit is enforced', () => {
  const Recursive = defineRecursive();
  let value: Record<string, unknown> = { value: 1 };
  for (let i = 0; i < 8; i++) value = { value: i, child: value };
  const bytes = encodeMessage(Recursive, value, { maxDepth: 16 });
  assert.throws(() => decodeMessage(Recursive, bytes, { maxDepth: 3 }), DecodeError);
});

function defineRecursive(): MessageDescriptor {
  const value: FieldDescriptor = { number: 1, name: 'value', type: 'sint32' };
  const child: FieldDescriptor = { number: 2, name: 'child', type: 'message' };
  const fields = [value, child];
  const desc: MessageDescriptor = {
    name: 'Recursive',
    fields,
    byNumber: new Map(fields.map((f) => [f.number, f])),
    byName: new Map(fields.map((f) => [f.name, f])),
  };
  child.message = desc;
  return desc;
}

test('decode of empty buffer yields empty object', () => {
  assert.deepEqual(decodeMessage(Address, new Uint8Array(0)), {});
});
