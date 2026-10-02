import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, type MessageSchema } from '../src/decoder.ts';
import { DecodeError, ErrorCode, WireType, WireWriter } from '../src/wire.ts';

const addressSchema: MessageSchema = {
  name: 'Address',
  fields: {
    1: { name: 'city', type: 'string' },
    2: { name: 'zip', type: 'uint32' },
  },
};

const personSchema: MessageSchema = {
  name: 'Person',
  fields: {
    1: { name: 'id', type: 'uint64' },
    2: { name: 'name', type: 'string' },
    3: { name: 'score', type: 'sint32' },
    4: { name: 'address', type: 'message', schema: addressSchema },
    5: { name: 'tags', type: 'string', repeated: true },
    6: { name: 'samples', type: 'sint32', repeated: true, packed: true },
    7: { name: 'weights', type: 'fixed32', repeated: true, packed: true },
    8: { name: 'active', type: 'bool' },
    9: { name: 'manager', type: 'message', schema: undefined }, // filled below (self-nest)
  },
};
(personSchema.fields[9] as { schema?: MessageSchema }).schema = personSchema;

function buildPerson(): Uint8Array {
  const address = new WireWriter().tag(1, WireType.LEN).string('Shanghai').tag(2, WireType.VARINT).varint(200000);
  const managerAddr = new WireWriter().tag(1, WireType.LEN).string('Beijing');
  const manager = new WireWriter()
    .tag(1, WireType.VARINT).varint(7n)
    .tag(2, WireType.LEN).string('boss')
    .message(4, managerAddr);
  const w = new WireWriter()
    .tag(1, WireType.VARINT).varint(9007199254740991n)
    .tag(2, WireType.LEN).string('alice')
    .tag(3, WireType.VARINT).zigZag32(-42)
    .message(4, address)
    .tag(5, WireType.LEN).string('x')
    .tag(5, WireType.LEN).string('y')
    .tag(8, WireType.VARINT).varint(1)
    .message(9, manager);
  // packed sint32: [-1, 0, 1, 300]
  const packed = new WireWriter().zigZag32(-1).zigZag32(0).zigZag32(1).zigZag32(300);
  w.tag(6, WireType.LEN).bytes(packed.finish());
  // packed fixed32
  const packedF = new WireWriter().fixed32(10).fixed32(20).fixed32(30);
  w.tag(7, WireType.LEN).bytes(packedF.finish());
  // unknown field 99 (should be skipped)
  w.tag(99, WireType.VARINT).varint(123);
  return w.finish();
}

test('decodes multi-level nested message with packed repeated fields', () => {
  const { value, stats } = decodeMessage(personSchema, buildPerson());
  assert.equal(value.id, 9007199254740991n);
  assert.equal(value.name, 'alice');
  assert.equal(value.score, -42);
  assert.deepEqual(value.address, { city: 'Shanghai', zip: 200000 });
  assert.deepEqual(value.tags, ['x', 'y']);
  assert.deepEqual(value.samples, [-1, 0, 1, 300]);
  assert.deepEqual(value.weights, [10, 20, 30]);
  assert.equal(value.active, true);
  assert.deepEqual(value.manager, { id: 7n, name: 'boss', address: { city: 'Beijing' } });
  assert.equal(stats.unknownFields, 1);
  assert.equal(stats.maxDepthReached, 2);
});

test('packed field also accepts unpacked (non-packed) encoding', () => {
  const w = new WireWriter()
    .tag(6, WireType.VARINT).zigZag32(5)
    .tag(6, WireType.VARINT).zigZag32(-5);
  const { value } = decodeMessage(personSchema, w.finish());
  assert.deepEqual(value.samples, [5, -5]);
});

test('multiple packed segments concatenate', () => {
  const seg = (vals: number[]) => {
    const s = new WireWriter();
    for (const v of vals) s.zigZag32(v);
    return s.finish();
  };
  const w = new WireWriter()
    .tag(6, WireType.LEN).bytes(seg([1, 2]))
    .tag(6, WireType.LEN).bytes(seg([3]));
  const { value } = decodeMessage(personSchema, w.finish());
  assert.deepEqual(value.samples, [1, 2, 3]);
});

test('truncated varint field reports safe bounds error', () => {
  const w = new WireWriter().tag(1, WireType.VARINT).byte(0x80); // unterminated
  assert.throws(
    () => decodeMessage(personSchema, w.finish()),
    (e: unknown) => {
      assert.ok(e instanceof DecodeError);
      assert.equal((e as DecodeError).code, ErrorCode.TRUNCATED);
      return true;
    },
  );
});

test('truncated nested message payload reports ERR_TRUNCATED', () => {
  const w = new WireWriter().tag(4, WireType.LEN).varint(50).byte(0x0a); // declares 50 bytes
  assert.throws(
    () => decodeMessage(personSchema, w.finish()),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
});

test('truncated packed segment reports ERR_TRUNCATED', () => {
  const w = new WireWriter().tag(6, WireType.LEN).varint(3).byte(0x80); // packed varint cut off
  assert.throws(
    () => decodeMessage(personSchema, w.finish()),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
});

test('truncated tag at stream tail reports ERR_TRUNCATED', () => {
  const w = new WireWriter().byte(0x98); // tag varint with continuation bit only
  assert.throws(
    () => decodeMessage(personSchema, w.finish()),
    (e: unknown) => (e as DecodeError).code === ErrorCode.TRUNCATED,
  );
});

test('wire-type mismatch against schema raises ERR_INVALID_WIRE_TYPE', () => {
  const w = new WireWriter().tag(1, WireType.I32).fixed32(1); // id expects VARINT
  assert.throws(
    () => decodeMessage(personSchema, w.finish()),
    (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_WIRE_TYPE,
  );
});

test('strict mode rejects unknown fields', () => {
  const w = new WireWriter().tag(77, WireType.VARINT).varint(1);
  assert.throws(
    () => decodeMessage(personSchema, w.finish(), { strict: true }),
    (e: unknown) => (e as DecodeError).code === ErrorCode.INVALID_TAG,
  );
  const { value } = decodeMessage(personSchema, w.finish());
  assert.deepEqual(value, {});
});

test('nesting depth limit guards against pathological payloads', () => {
  // self-nested manager chain deeper than maxDepth
  let payload = new WireWriter().finish();
  for (let i = 0; i < 10; i++) {
    payload = new WireWriter().tag(9, WireType.LEN).bytes(payload).finish();
  }
  assert.throws(
    () => decodeMessage(personSchema, payload, { maxDepth: 5 }),
    (e: unknown) => (e as DecodeError).code === ErrorCode.NESTING_TOO_DEEP,
  );
});

test('empty stream decodes to empty struct', () => {
  const { value, stats } = decodeMessage(personSchema, new Uint8Array(0));
  assert.deepEqual(value, {});
  assert.equal(stats.knownFields, 0);
});
