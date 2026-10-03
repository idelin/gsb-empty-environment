import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage, validateSchema } from '../src/schema.ts';
import type { MessageSchema } from '../src/schema.ts';
import { MalformedWireError, TruncationError, WireWriter } from '../src/wire.ts';

const addressSchema: MessageSchema = {
  name: 'Address',
  fields: {
    1: { name: 'city', type: 'string' },
    2: { name: 'zip', type: 'fixed32' },
    3: { name: 'coords', type: 'sfixed64', repeated: true, packed: true },
  },
};

const orderSchema: MessageSchema = {
  name: 'Order',
  fields: {
    1: { name: 'id', type: 'uint64' },
    2: { name: 'note', type: 'string' },
    3: { name: 'balance', type: 'sint64' },
    4: { name: 'scores', type: 'int32', repeated: true, packed: true },
    5: { name: 'address', type: 'message', message: addressSchema },
    6: { name: 'avatar', type: 'bytes' },
    7: { name: 'vip', type: 'bool' },
    8: { name: 'ratio', type: 'double' },
  },
};

const envelopeSchema: MessageSchema = {
  name: 'Envelope',
  fields: {
    1: { name: 'order', type: 'message', message: orderSchema },
    2: { name: 'tags', type: 'string', repeated: true },
  },
};

function sampleOrder() {
  return {
    id: 18446744073709551615n,
    note: 'hello, 世界',
    balance: -1234567890123456789n,
    scores: [0, -1, 127, -128, 300, -2147483648, 2147483647],
    address: {
      city: '上海',
      zip: 200000,
      coords: [121473701n, -31161180n, 0n],
    },
    avatar: Uint8Array.from([0xde, 0xad, 0xbe, 0xef]),
    vip: true,
    ratio: 0.125,
  };
}

test('round-trips a deeply nested message with packed repeated fields', () => {
  const envelope = { order: sampleOrder(), tags: ['a', 'b', 'c'] };
  const bytes = encodeMessage(envelopeSchema, envelope);
  const decoded = decodeMessage(envelopeSchema, bytes);
  assert.deepEqual(decoded, { order: sampleOrder(), tags: ['a', 'b', 'c'] });
});

test('packed repeated scalars use a single length-delimited run', () => {
  const bytes = encodeMessage(orderSchema, { scores: [1, 2, 3] });
  // tag(4, wt2)=0x22, len=3, then three varints
  assert.deepEqual([...bytes], [0x22, 0x03, 0x01, 0x02, 0x03]);
  assert.deepEqual(decodeMessage(orderSchema, bytes).scores, [1, 2, 3]);
});

test('decoder accepts unpacked encoding for packable repeated fields', () => {
  const writer = new WireWriter();
  for (const score of [5, 6, 7]) {
    writer.writeTag(4, 0); // int32, varint, unpacked
    writer.writeVarint(score);
  }
  const decoded = decodeMessage(orderSchema, writer.finish());
  assert.deepEqual(decoded.scores, [5, 6, 7]);
});

test('decoder accepts packed encoding even when schema says unpacked', () => {
  const schema: MessageSchema = {
    name: 'S',
    fields: { 1: { name: 'nums', type: 'uint32', repeated: true } },
  };
  const writer = new WireWriter();
  writer.writeTag(1, 2);
  writer.writeBytes(Uint8Array.from([1, 2, 3]));
  assert.deepEqual(decodeMessage(schema, writer.finish()).nums, [1, 2, 3]);
});

test('missing repeated fields default to empty arrays', () => {
  const decoded = decodeMessage(orderSchema, encodeMessage(orderSchema, { vip: false }));
  assert.deepEqual(decoded.scores, []);
  assert.equal(decoded.vip, false);
});

test('unknown fields are skipped across all wire types', () => {
  const writer = new WireWriter();
  writer.writeTag(1, 0).writeVarint(42n); // known: id
  writer.writeTag(100, 0).writeVarint(1n); // unknown varint
  writer.writeTag(101, 1).writeFixed64(7n); // unknown fixed64
  writer.writeTag(102, 2).writeBytes(Uint8Array.from([9, 9])); // unknown length-delimited
  writer.writeTag(103, 5).writeFixed32(3); // unknown fixed32
  const decoded = decodeMessage(orderSchema, writer.finish());
  assert.equal(decoded.id, 42n);
  assert.deepEqual(Object.keys(decoded).sort(), ['id', 'scores'].sort());
});

test('truncated nested message payload raises TruncationError with field path', () => {
  const bytes = encodeMessage(envelopeSchema, { order: sampleOrder(), tags: [] });
  const truncated = bytes.subarray(0, bytes.length - 5);
  assert.throws(() => decodeMessage(envelopeSchema, truncated), (error: unknown) => {
    assert.ok(error instanceof TruncationError);
    assert.match(error.context, /Envelope\.order|Order\./);
    return true;
  });
});

test('truncated packed repeated array is guarded', () => {
  const writer = new WireWriter();
  writer.writeTag(4, 2);
  writer.writeVarint(10); // declares 10 packed bytes
  writer.writeRawBytes(Uint8Array.from([1, 2])); // provides 2
  assert.throws(() => decodeMessage(orderSchema, writer.finish()), (error: unknown) => {
    assert.ok(error instanceof TruncationError);
    assert.match(error.context, /Order\.scores/);
    return true;
  });
});

test('packed payload cut mid-varint is guarded', () => {
  const writer = new WireWriter();
  writer.writeTag(4, 2);
  writer.writeBytes(Uint8Array.from([0x80])); // dangling continuation byte
  assert.throws(() => decodeMessage(orderSchema, writer.finish()), TruncationError);
});

test('truncated tag at end of buffer is guarded', () => {
  assert.throws(() => decodeMessage(orderSchema, Uint8Array.from([0x80])), TruncationError);
});

test('wire-type mismatch is reported', () => {
  const writer = new WireWriter();
  writer.writeTag(1, 5).writeFixed32(1); // id expects varint (0)
  assert.throws(() => decodeMessage(orderSchema, writer.finish()), MalformedWireError);
});

test('field number zero is rejected', () => {
  assert.throws(() => decodeMessage(orderSchema, Uint8Array.from([0x00])), MalformedWireError);
});

test('schema validation rejects bad descriptors', () => {
  assert.throws(
    () => validateSchema({ name: 'X', fields: { 1: { name: 'm', type: 'message' } } }),
    MalformedWireError,
  );
  assert.throws(
    () => validateSchema({ name: 'X', fields: { 1: { name: 'p', type: 'string', repeated: true, packed: true } } }),
    MalformedWireError,
  );
  assert.throws(
    () => validateSchema({ name: 'X', fields: { 0: { name: 'z', type: 'bool' } } }),
    MalformedWireError,
  );
});

test('empty message decodes to defaults only', () => {
  assert.deepEqual(decodeMessage(orderSchema, new Uint8Array(0)), { scores: [] });
});

test('bytes and string fields are distinct on the wire', () => {
  const value = { note: 'abc', avatar: Uint8Array.from([0x61, 0x62, 0x63]) };
  const decoded = decodeMessage(orderSchema, encodeMessage(orderSchema, value));
  assert.equal(decoded.note, 'abc');
  assert.ok(decoded.avatar instanceof Uint8Array);
  assert.deepEqual([...(decoded.avatar as Uint8Array)], [0x61, 0x62, 0x63]);
});
