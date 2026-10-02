import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage, type MessageDescriptor } from '../src/index.ts';
import { Person, samplePerson } from '../fixtures/schemas.ts';

test('encode/decode round-trip preserves all field values', () => {
  const decoded = decodeMessage(Person, encodeMessage(Person, samplePerson));
  assert.deepEqual(decoded, samplePerson);
});

test('round-trip of all scalar types at boundaries', () => {
  const Scalars: MessageDescriptor = {
    name: 'Scalars',
    fields: [
      { no: 1, name: 'i32', type: 'int32' },
      { no: 2, name: 'i64', type: 'int64' },
      { no: 3, name: 'u32', type: 'uint32' },
      { no: 4, name: 'u64', type: 'uint64' },
      { no: 5, name: 's32', type: 'sint32' },
      { no: 6, name: 's64', type: 'sint64' },
      { no: 7, name: 'b', type: 'bool' },
      { no: 8, name: 'f32', type: 'fixed32' },
      { no: 9, name: 'f64', type: 'fixed64' },
      { no: 10, name: 'sf32', type: 'sfixed32' },
      { no: 11, name: 'sf64', type: 'sfixed64' },
      { no: 12, name: 'fl', type: 'float' },
      { no: 13, name: 'd', type: 'double' },
      { no: 14, name: 's', type: 'string' },
      { no: 15, name: 'by', type: 'bytes' },
      { no: 16, name: 'e', type: 'enum' },
    ],
  };
  const obj = {
    i32: -2147483648,
    i64: -(1n << 63n),
    u32: 4294967295,
    u64: (1n << 64n) - 1n,
    s32: 2147483647,
    s64: (1n << 63n) - 1n,
    b: true,
    f32: 4294967295,
    f64: (1n << 64n) - 1n,
    sf32: -2147483648,
    sf64: -(1n << 63n),
    fl: 3.140000104904175,
    d: -2.2250738585072014e-308,
    s: 'hello 世界 🌍',
    by: new Uint8Array([0, 1, 255, 128]),
    e: 3,
  };
  assert.deepEqual(decodeMessage(Scalars, encodeMessage(Scalars, obj)), obj);
});

test('negative int32 encodes as 10-byte varint per spec', () => {
  const M: MessageDescriptor = { name: 'M', fields: [{ no: 1, name: 'v', type: 'int32' }] };
  const bytes = encodeMessage(M, { v: -1 });
  assert.equal(bytes.length, 11); // 1 tag + 10 varint bytes
  assert.deepEqual(decodeMessage(M, bytes), { v: -1 });
});

test('empty repeated fields are omitted and decode to absent', () => {
  const decoded = decodeMessage(Person, encodeMessage(Person, { name: 'x', scores: [] }));
  assert.deepEqual(decoded, { name: 'x' });
});

test('encoding is deterministic across runs', () => {
  const a = encodeMessage(Person, samplePerson);
  const b = encodeMessage(Person, samplePerson);
  assert.deepEqual(a, b);
});

test('repeated messages and strings accumulate across occurrences', () => {
  const decoded = decodeMessage(Person, encodeMessage(Person, {
    aliases: ['x', 'y', 'z'],
    previous: [{ city: 'A' }, { city: 'B' }],
  }));
  assert.deepEqual(decoded, { aliases: ['x', 'y', 'z'], previous: [{ city: 'A' }, { city: 'B' }] });
});
