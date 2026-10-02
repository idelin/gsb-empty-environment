import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeMessage,
  encodeMessage,
  validateMessage,
  type MessageSchema,
} from '../src/schema.ts';
import { MalformedMessageError, ValidationError } from '../src/errors.ts';
import { WireWriter } from '../src/writer.ts';
import { WireType } from '../src/reader.ts';

const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'street', number: 1, type: 'string', required: true, maxLength: 128 },
    { name: 'zip', number: 2, type: 'sint32', min: 0, max: 99999 },
  ],
};

const Person: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'name', number: 2, type: 'string', required: true, maxLength: 64 },
    { name: 'email', number: 3, type: 'string' },
    { name: 'scores', number: 4, type: 'sint32', repeated: true }, // packed by default
    { name: 'tags', number: 5, type: 'string', repeated: true },
    { name: 'address', number: 6, type: 'message', message: Address },
    { name: 'previous', number: 7, type: 'message', message: Address, repeated: true },
    { name: 'active', number: 8, type: 'bool', default: true },
    { name: 'delta', number: 9, type: 'sint64' },
    { name: 'ratio', number: 10, type: 'double' },
    { name: 'flags', number: 11, type: 'fixed32' },
    { name: 'big', number: 12, type: 'sfixed64' },
    { name: 'mood', number: 13, type: 'enum' },
  ],
};

function samplePerson(): Record<string, unknown> {
  return {
    id: 18446744073709551615n, // uint64 max
    name: 'Ada Lovelace ✨',
    email: 'ada@example.com',
    scores: [-5, 0, 7, 123456, -2147483648, 2147483647],
    tags: ['admin', '早期用户', ''],
    address: { street: '12 Analytical Engine Rd', zip: 10001 },
    previous: [
      { street: '1 First St', zip: 1 },
      { street: '2 Second Ave', zip: 99999 },
    ],
    active: false,
    delta: -9223372036854775808n, // int64 min
    ratio: Math.PI,
    flags: 0xdeadbeef,
    big: 9223372036854775807n, // int64 max
    mood: 2,
  };
}

test('round-trip: nested messages + packed repeated + all scalar kinds', () => {
  const original = samplePerson();
  const bytes = encodeMessage(Person, original);
  const decoded = decodeMessage(Person, bytes);
  assert.deepEqual(decoded, original);
});

test('packed repeated: wire form is a single LEN field', () => {
  const bytes = encodeMessage(Person, {
    id: 1n,
    name: 'x',
    scores: [1, 2, 3],
    active: true,
  });
  // tag of field 4 with wire type LEN === (4 << 3) | 2 === 0x22
  const idx = bytes.indexOf(0x22);
  assert.ok(idx >= 0, 'packed tag present');
  assert.equal(bytes[idx + 1], 3, 'three 1-byte zigzag values packed');
});

test('packed repeated: decoder accepts BOTH packed and unpacked encodings', () => {
  const packed = decodeMessage(Person, encodeMessage(Person, { id: 1n, name: 'x', scores: [1, -2, 3] }));
  assert.deepEqual(packed.scores, [1, -2, 3]);

  // Hand-craft the unpacked form: three separate field-4 VARINT entries.
  const w = new WireWriter();
  w.writeTag(1, WireType.VARINT).writeVarint(1n); // id
  w.writeTag(2, WireType.LEN).writeString('x'); // name
  for (const s of [1, -2, 3]) {
    w.writeTag(4, WireType.VARINT);
    w.writeVarint(BigInt((s << 1) ^ (s >> 31)) & 0xffffffffn);
  }
  const unpacked = decodeMessage(Person, w.finish());
  assert.deepEqual(unpacked.scores, [1, -2, 3]);
  assert.deepEqual(unpacked, packed);
});

test('deep nesting: 5-level message chain round-trips', () => {
  const level = (depth: number): MessageSchema => ({
    name: `L${depth}`,
    fields: [
      { name: 'val', number: 1, type: 'int32', required: true },
      ...(depth > 0
        ? [{ name: 'child', number: 2, type: 'message', message: level(depth - 1) } as const]
        : []),
    ],
  });
  const schema = level(5);
  const value = {
    val: 5,
    child: { val: 4, child: { val: 3, child: { val: 2, child: { val: 1, child: { val: 0 } } } } },
  };
  const decoded = decodeMessage(schema, encodeMessage(schema, value));
  assert.deepEqual(decoded, value);
});

test('depth guard: cyclic payload beyond maxDepth is rejected safely', () => {
  const Node: MessageSchema = { name: 'Node', fields: [] };
  Node.fields.push({ name: 'child', number: 1, type: 'message', message: Node });
  // Manually build an arbitrarily deep payload: tag 0x0a + len + nested...
  const depth = 500;
  const sizes: number[] = new Array(depth).fill(0);
  let payload = new Uint8Array(0);
  for (let i = 0; i < depth; i++) {
    const w = new WireWriter();
    w.writeTag(1, WireType.LEN).writeLengthDelimited(payload);
    payload = w.finish();
    sizes[i] = payload.length;
  }
  assert.throws(() => decodeMessage(Node, payload, { maxDepth: 64 }), MalformedMessageError);
  // and with a generous limit it decodes fine
  const decoded = decodeMessage(Node, payload, { maxDepth: 1000 });
  let cursor: Record<string, unknown> = decoded;
  let count = 0;
  while (cursor.child) {
    cursor = cursor.child as Record<string, unknown>;
    count++;
  }
  assert.equal(count, depth);
});

test('unknown fields are skipped without disturbing known fields', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.VARINT).writeVarint(42n); // id
  w.writeTag(2, WireType.LEN).writeString('n'); // name
  w.writeTag(100, WireType.VARINT).writeVarint(999n); // unknown varint
  w.writeTag(101, WireType.FIXED64).writeFixed64(123n); // unknown fixed64
  w.writeTag(102, WireType.LEN).writeLengthDelimited(Uint8Array.of(1, 2, 3)); // unknown len
  w.writeTag(103, WireType.FIXED32).writeFixed32(7); // unknown fixed32
  const decoded = decodeMessage(Person, w.finish());
  assert.equal(decoded.id, 42n);
  assert.equal(decoded.name, 'n');
  assert.equal(decoded.active, true, 'default applied');
  assert.equal('100' in decoded, false);
});

test('defaults fill absent optional fields; explicit values win', () => {
  const decoded = decodeMessage(Person, encodeMessage(Person, { id: 7n, name: 'd' }));
  assert.equal(decoded.active, true);
  const decoded2 = decodeMessage(Person, encodeMessage(Person, { id: 7n, name: 'd', active: false }));
  assert.equal(decoded2.active, false);
});

test('validation engine: required, type, range, maxLength, custom predicate', () => {
  const Schema: MessageSchema = {
    name: 'S',
    fields: [
      { name: 'req', number: 1, type: 'string', required: true },
      { name: 'count', number: 2, type: 'int32', min: 0, max: 10 },
      { name: 'label', number: 3, type: 'string', maxLength: 3 },
      { name: 'even', number: 4, type: 'int32', validate: (v) => (v as number) % 2 === 0 || 'must be even' },
    ],
  };
  assert.throws(
    () => validateMessage(Schema, { count: 99, label: 'toolong', even: 3 }),
    (err: unknown) => {
      assert.ok(err instanceof ValidationError);
      assert.equal(err.issues.length, 4, JSON.stringify(err.issues));
      assert.ok(err.issues.some((i) => i.includes('required field is missing')));
      assert.ok(err.issues.some((i) => i.includes('> max 10')));
      assert.ok(err.issues.some((i) => i.includes('maxLength 3')));
      assert.ok(err.issues.some((i) => i.includes('must be even')));
      return true;
    },
  );
  assert.doesNotThrow(() => validateMessage(Schema, { req: 'ok', count: 5, label: 'abc', even: 4 }));
});

test('validation engine: nested paths and repeated element indexing in issues', () => {
  assert.throws(
    () =>
      validateMessage(Person, {
        id: 1n,
        name: 'x',
        address: { street: 's', zip: 100000 },
        previous: [{ street: 'ok' }, { street: 42 }],
      }),
    (err: unknown) => {
      assert.ok(err instanceof ValidationError);
      assert.ok(err.issues.some((i) => i.startsWith('address.zip:')), JSON.stringify(err.issues));
      assert.ok(err.issues.some((i) => i.startsWith('previous[1].street:')), JSON.stringify(err.issues));
      return true;
    },
  );
});

test('encode rejects invalid values instead of emitting garbage', () => {
  assert.throws(() => encodeMessage(Person, { id: 1n }), ValidationError); // missing required name
  assert.throws(
    () => encodeMessage(Person, { id: 1n, name: 'x', scores: ['not-a-number'] }),
    ValidationError,
  );
});

test('wire-type mismatch for a known field is a malformed error', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.FIXED32).writeFixed32(1); // id declared as VARINT
  assert.throws(() => decodeMessage(Person, w.finish()), MalformedMessageError);
});

test('invalid UTF-8 in string field is rejected', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.VARINT).writeVarint(1n);
  w.writeTag(2, WireType.LEN).writeLengthDelimited(Uint8Array.of(0xff, 0xfe));
  assert.throws(() => decodeMessage(Person, w.finish()), MalformedMessageError);
});

test('empty message and empty repeated fields encode to minimal bytes', () => {
  const bytes = encodeMessage(Person, { id: 0n, name: '', scores: [], tags: [] });
  const decoded = decodeMessage(Person, bytes);
  assert.equal(decoded.id, 0n);
  assert.equal(decoded.name, '');
  assert.equal(decoded.scores, undefined, 'empty repeated is omitted on the wire');
});
