import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReflectionEngine, type MessageSchema } from '../src/schema.ts';
import { WireWriter, concatBytes } from '../src/wire/writer.ts';
import { writeVarint, zigzagEncode } from '../src/wire/varint.ts';
import { ProtobufDecodeError, SchemaValidationError } from '../src/wire/errors.ts';

const addressSchema: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'city', number: 1, type: 'string', required: true },
    { name: 'zip', number: 2, type: 'string' },
  ],
};

const personSchema: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'name', number: 2, type: 'string', required: true },
    { name: 'email', number: 3, type: 'string' },
    { name: 'age', number: 4, type: 'sint32' },
    { name: 'score', number: 5, type: 'sfixed64' },
    { name: 'flags', number: 6, type: 'fixed32' },
    { name: 'address', number: 7, type: 'message', messageType: addressSchema },
    { name: 'aliases', number: 8, type: 'string', repeated: true },
    { name: 'luckyNumbers', number: 9, type: 'int32', repeated: true, packed: true },
    { name: 'tags', number: 10, type: 'enum', repeated: true, packed: true },
    { name: 'active', number: 11, type: 'bool' },
  ],
};

function samplePersonBytes(): Uint8Array {
  const address = new WireWriter().stringField(1, 'Shanghai').stringField(2, '200000').finish();
  const packed = concatBytes(writeVarint(7), writeVarint(2n ** 32n - 1n), writeVarint(300));
  const packedTags = concatBytes(writeVarint(1), writeVarint(2));
  return new WireWriter()
    .varintField(1, 9001)
    .stringField(2, 'Ada')
    .stringField(3, 'ada@example.com')
    .varintField(4, zigzagEncode(-36))
    .fixed64Field(5, 2n ** 64n - 2n)
    .fixed32Field(6, 0xcafebabe)
    .messageField(7, address)
    .stringField(8, 'ada')
    .stringField(8, 'countess')
    .packedField(9, packed)
    .packedField(10, packedTags)
    .varintField(11, 1)
    .finish();
}

test('reflection engine decodes a fully-populated nested message', () => {
  const engine = new ReflectionEngine();
  engine.registerSchema(personSchema);
  const decoded = engine.decode(samplePersonBytes(), personSchema);

  assert.equal(decoded.id, 9001n);
  assert.equal(decoded.name, 'Ada');
  assert.equal(decoded.email, 'ada@example.com');
  assert.equal(decoded.age, -36);
  assert.equal(decoded.score, -2n); // sfixed64 reinterpretation
  assert.equal(decoded.flags, 0xcafebabe);
  assert.deepEqual(decoded.aliases, ['ada', 'countess']);
  assert.deepEqual(decoded.luckyNumbers, [7, -1, 300]);
  assert.deepEqual(decoded.tags, [1, 2]);
  assert.equal(decoded.active, true);
  const address = decoded.address as Record<string, unknown>;
  assert.equal(address.city, 'Shanghai');
  assert.equal(address.zip, '200000');
});

test('repeated nested messages accumulate in order', () => {
  const schema: MessageSchema = {
    name: 'Bag',
    fields: [{ name: 'items', number: 1, type: 'message', messageType: addressSchema, repeated: true }],
  };
  const a = new WireWriter().stringField(1, 'A').finish();
  const b = new WireWriter().stringField(1, 'B').finish();
  const buf = new WireWriter().messageField(1, a).messageField(1, b).finish();
  const decoded = new ReflectionEngine().decode(buf, schema);
  const items = decoded.items as Array<Record<string, unknown>>;
  assert.deepEqual(items.map((i) => i.city), ['A', 'B']);
});

test('missing required field fails validation', () => {
  const buf = new WireWriter().stringField(2, 'Ada').finish(); // no id (field 1)
  assert.throws(() => new ReflectionEngine().decode(buf, personSchema), SchemaValidationError);
});

test('wire-type mismatch on a known field is a decode error', () => {
  const buf = new WireWriter().varintField(1, 1).fixed32Field(2, 0).finish(); // name as fixed32
  assert.throws(() => new ReflectionEngine().decode(buf, personSchema), ProtobufDecodeError);
});

test('unknown fields are collected by default and rejected in strict mode', () => {
  const buf = new WireWriter()
    .varintField(1, 1)
    .stringField(2, 'Ada')
    .varintField(99, 123) // unknown
    .finish();
  const engine = new ReflectionEngine();
  const decoded = engine.decode(buf, personSchema);
  assert.deepEqual(decoded.$unknownFields, [{ fieldNumber: 99, wireType: 0 }]);
  assert.throws(
    () => engine.decode(buf, personSchema, { allowUnknownFields: false }),
    SchemaValidationError,
  );
});

test('schema registration rejects duplicate numbers and reserved range', () => {
  const engine = new ReflectionEngine();
  assert.throws(
    () =>
      engine.registerSchema({
        name: 'Bad',
        fields: [
          { name: 'a', number: 1, type: 'int32' },
          { name: 'b', number: 1, type: 'int32' },
        ],
      }),
    SchemaValidationError,
  );
  assert.throws(
    () =>
      engine.registerSchema({
        name: 'Bad2',
        fields: [{ name: 'a', number: 19000, type: 'int32' }],
      }),
    SchemaValidationError,
  );
  assert.throws(
    () =>
      engine.registerSchema({
        name: 'Bad3',
        fields: [{ name: 'a', number: 1, type: 'string', packed: true }],
      }),
    SchemaValidationError,
  );
});

test('nesting depth limit guards against pathological recursion', () => {
  const schema: MessageSchema = {
    name: 'Node',
    fields: [{ name: 'child', number: 1, type: 'message', messageType: undefined as never }],
  };
  schema.fields[0].messageType = schema; // self-recursive schema
  let payload = new Uint8Array(0);
  for (let i = 0; i < 40; i++) {
    payload = new WireWriter().messageField(1, payload).finish();
  }
  assert.throws(() => new ReflectionEngine().decode(payload, schema), /nesting depth/);
});

test('invalid UTF-8 in a string field is reported as decode error', () => {
  const buf = new WireWriter()
    .varintField(1, 1)
    .bytesField(2, Uint8Array.from([0xff, 0xfe]))
    .finish();
  assert.throws(() => new ReflectionEngine().decode(buf, personSchema), ProtobufDecodeError);
});
