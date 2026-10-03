import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defineSchema, MessageDecoder, DecodeError, TruncatedError } from '../src/decoder.ts';
import { encode } from '../src/encoder.ts';
import { WireReader, WireWriter, WireType } from '../src/wire.ts';

const Address = defineSchema('Address', [
  { no: 1, name: 'city', type: 'string' },
  { no: 2, name: 'zip', type: 'fixed32' },
]);

const Level3 = defineSchema('Level3', [
  { no: 1, name: 'value', type: 'varint' },
  { no: 2, name: 'flag', type: 'bool' },
]);

const Level2 = defineSchema('Level2', [
  { no: 1, name: 'inner', type: 'message', schema: Level3 },
  { no: 2, name: 'label', type: 'string' },
]);

const Level1 = defineSchema('Level1', [
  { no: 1, name: 'child', type: 'message', schema: Level2 },
]);

const Person = defineSchema('Person', [
  { no: 1, name: 'name', type: 'string' },
  { no: 2, name: 'age', type: 'zigzag32' },
  { no: 3, name: 'address', type: 'message', schema: Address },
  { no: 4, name: 'scores', type: 'varint', repeated: true, packed: true },
  { no: 5, name: 'tags', type: 'string', repeated: true },
  { no: 6, name: 'aliases', type: 'message', repeated: true, schema: Address },
  { no: 7, name: 'balance', type: 'zigzag64' },
  { no: 8, name: 'id', type: 'fixed64' },
  { no: 9, name: 'ratios', type: 'fixed32', repeated: true, packed: true },
]);

test('schema validation: rejects invalid definitions', () => {
  assert.throws(() => defineSchema('Empty', []), DecodeError);
  assert.throws(() => defineSchema('Dup', [
    { no: 1, name: 'a', type: 'varint' },
    { no: 1, name: 'b', type: 'varint' },
  ]), DecodeError);
  assert.throws(() => defineSchema('BadNo', [{ no: 0, name: 'a', type: 'varint' }]), DecodeError);
  assert.throws(() => defineSchema('NoNested', [{ no: 1, name: 'm', type: 'message' }]), DecodeError);
  assert.throws(() => defineSchema('BadPacked', [
    { no: 1, name: 's', type: 'string', repeated: true, packed: true },
  ]), DecodeError);
  assert.throws(() => defineSchema('PackedSingle', [
    { no: 1, name: 'v', type: 'varint', packed: true },
  ]), DecodeError);
});

test('round-trip: nested messages, packed arrays, all scalar types', () => {
  const decoder = new MessageDecoder(Person);
  const source = {
    name: 'Ada',
    age: -36,
    address: { city: 'Shanghai', zip: 200000 },
    scores: [0, 1, 127, 128, 300, 2 ** 40],
    tags: ['x', 'y', 'z'],
    aliases: [{ city: 'Beijing', zip: 100000 }, { city: 'Shenzhen', zip: 518000 }],
    balance: -(2n ** 60n),
    id: 0xffff_ffff_ffff_ffffn,
    ratios: [1, 4294967295, 256],
  };
  const bytes = encode(Person, source);
  const msg = decoder.decode(bytes) as Record<string, unknown>;
  assert.equal(msg.name, 'Ada');
  assert.equal(msg.age, -36);
  assert.deepEqual(msg.address, { city: 'Shanghai', zip: 200000 });
  assert.deepEqual(msg.scores, [0, 1, 127, 128, 300, 2 ** 40]);
  assert.deepEqual(msg.tags, ['x', 'y', 'z']);
  assert.deepEqual(msg.aliases, [
    { city: 'Beijing', zip: 100000 },
    { city: 'Shenzhen', zip: 518000 },
  ]);
  assert.equal(msg.balance, -(2n ** 60n));
  assert.equal(msg.id, 0xffff_ffff_ffff_ffffn);
  assert.deepEqual(msg.ratios, [1, 4294967295, 256]);
  decoder.release(msg);
  decoder.assertNoLeaks();
});

test('multi-level nesting: 3 levels deep', () => {
  const decoder = new MessageDecoder(Level1);
  const bytes = encode(Level1, { child: { inner: { value: 42, flag: true }, label: 'deep' } });
  const msg = decoder.decode(bytes) as Record<string, unknown>;
  assert.deepEqual(msg, { child: { inner: { value: 42, flag: true }, label: 'deep' } });
  decoder.release(msg);
  decoder.assertNoLeaks();
});

test('packed field also accepts unpacked (per-field) encoding', () => {
  const writer = new WireWriter();
  for (const v of [5, 6, 7]) writer.writeTag(4, WireType.Varint).writeVarint(v);
  const decoder = new MessageDecoder(Person);
  const msg = decoder.decode(writer.finish()) as Record<string, unknown>;
  assert.deepEqual(msg.scores, [5, 6, 7]);
  decoder.release(msg);
  decoder.assertNoLeaks();
});

test('unknown fields are skipped without failing', () => {
  const writer = new WireWriter();
  writer.writeTag(1, WireType.LengthDelimited).writeString('Ada');
  writer.writeTag(100, WireType.Varint).writeVarint(1);
  writer.writeTag(101, WireType.Fixed64).writeFixed64(9n);
  writer.writeTag(102, WireType.LengthDelimited).writeString('unknown');
  writer.writeTag(103, WireType.Fixed32).writeFixed32(3);
  const decoder = new MessageDecoder(Person);
  const msg = decoder.decode(writer.finish()) as Record<string, unknown>;
  assert.deepEqual(msg, { name: 'Ada' });
  decoder.release(msg);
  decoder.assertNoLeaks();
});

test('truncation: every strict prefix of a nested message fails safely', () => {
  const decoder = new MessageDecoder(Level1);
  const bytes = encode(Level1, { child: { inner: { value: 300, flag: true }, label: 'deep' } });
  // cut=0 is a valid empty message; every other strict prefix truncates the
  // declared length-delimited payload and must fail safely.
  for (let cut = 1; cut < bytes.length; cut++) {
    assert.throws(
      () => decoder.decode(bytes.subarray(0, cut)),
      TruncatedError,
      `prefix of length ${cut} must raise TruncatedError`,
    );
    decoder.assertNoLeaks();
  }
});

test('truncation: cut inside packed repeated payload', () => {
  const decoder = new MessageDecoder(Person);
  const bytes = encode(Person, { scores: [1, 2, 300, 4, 5] });
  let sawTruncated = 0;
  for (let cut = 0; cut < bytes.length; cut++) {
    try {
      const msg = decoder.decode(bytes.subarray(0, cut));
      decoder.release(msg);
    } catch (err) {
      assert.ok(err instanceof TruncatedError, `cut=${cut}: ${err}`);
      sawTruncated++;
    }
    decoder.assertNoLeaks();
  }
  assert.ok(sawTruncated > 0, 'expected at least one truncating cut');
});

test('hostile inputs: oversized length and bad wire type are rejected', () => {
  const decoder = new MessageDecoder(Person);
  // field 1 (string) with length 0xFFFFFFFF
  const hostile = new WireWriter();
  hostile.writeTag(1, WireType.LengthDelimited).writeVarint(0xffff_ffff);
  assert.throws(() => decoder.decode(hostile.finish()), TruncatedError);
  decoder.assertNoLeaks();
  // field 2 (zigzag32, expects varint) encoded as fixed32
  const wrongWire = new WireWriter();
  wrongWire.writeTag(2, WireType.Fixed32).writeFixed32(1);
  assert.throws(() => decoder.decode(wrongWire.finish()), DecodeError);
  decoder.assertNoLeaks();
});

test('error inside nested message releases all partial allocations', () => {
  const decoder = new MessageDecoder(Person);
  // valid address header, then a truncated nested payload
  const writer = new WireWriter();
  writer.writeTag(1, WireType.LengthDelimited).writeString('Ada');
  writer.writeTag(3, WireType.LengthDelimited).writeVarint(10); // declares 10 bytes
  writer.writeBytes(new Uint8Array([0x0a, 0x03, 0x61])); // only 3 follow
  assert.throws(() => decoder.decode(writer.finish()), TruncatedError);
  decoder.assertNoLeaks();
});

test('zero-allocation reuse: pooled objects and arrays are recycled', () => {
  const decoder = new MessageDecoder(Person);
  const bytes = encode(Person, {
    name: 'Ada',
    address: { city: 'Shanghai', zip: 1 },
    scores: [1, 2, 3],
    tags: ['a'],
  });
  const first = decoder.decode(bytes) as Record<string, unknown>;
  const firstScores = first.scores;
  const firstTags = first.tags;
  decoder.release(first);
  const second = decoder.decode(bytes) as Record<string, unknown>;
  assert.equal(second, first, 'root message object must be reused from the pool');
  // pool is LIFO: the recycled array must be one of the previously released ones
  assert.ok(
    second.scores === firstScores || second.scores === firstTags,
    'repeated-field array must be reused from the pool',
  );
  decoder.release(second);
  decoder.assertNoLeaks();
  const stats = decoder.stats as { messages: Record<string, { reused: number }> };
  assert.ok(stats.messages.Person.reused >= 1);
});

test('steady-state decode loop keeps pool size bounded', () => {
  const decoder = new MessageDecoder(Person);
  const bytes = encode(Person, { name: 'x', scores: [1, 2, 3], address: { city: 'c', zip: 9 } });
  for (let i = 0; i < 10_000; i++) {
    decoder.release(decoder.decode(bytes));
  }
  decoder.assertNoLeaks();
  const stats = decoder.stats as {
    messages: Record<string, { pooled: number }>;
    arrays: { pooled: number };
  };
  assert.ok(stats.messages.Person.pooled <= 1);
  assert.ok(stats.arrays.pooled <= 1);
});

test('hand-crafted byte stream parses to expected structure', () => {
  // Person { name: "Bo", age: -1, scores: [1,2] } built byte-by-byte
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeString('Bo');
  w.writeTag(2, WireType.Varint).writeVarint(1); // zigzag(-1) = 1
  w.writeTag(4, WireType.LengthDelimited).writeBytes(new Uint8Array([0x01, 0x02]));
  const decoder = new MessageDecoder(Person);
  const msg = decoder.decode(w.finish()) as Record<string, unknown>;
  assert.equal(msg.name, 'Bo');
  assert.equal(msg.age, -1);
  assert.deepEqual(msg.scores, [1, 2]);
  decoder.release(msg);
  decoder.assertNoLeaks();
});

test('empty buffer decodes to empty message', () => {
  const decoder = new MessageDecoder(Person);
  const msg = decoder.decode(new Uint8Array(0));
  assert.deepEqual(msg, {});
  decoder.release(msg);
  decoder.assertNoLeaks();
});
