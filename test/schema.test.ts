import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { DecodeError, WireReader, WireType, WireWriter } from '../src/wire.ts';
import { SchemaError, decodeMessage, type Schema } from '../src/schema.ts';

/* A 3-level nested schema exercising every codec family. */
const ItemSchema: Schema = [
  { no: 1, name: 'sku', type: 'string' },
  { no: 2, name: 'qty', type: 'uint32' },
  { no: 3, name: 'price', type: 'double' },
];

const OrderSchema: Schema = [
  { no: 1, name: 'orderId', type: 'string' },
  { no: 2, name: 'items', type: 'message', repeated: true, schema: ItemSchema },
  { no: 3, name: 'tags', type: 'int32', repeated: true, packed: true },
  { no: 4, name: 'delta', type: 'sint64' },
  { no: 5, name: 'checksum', type: 'fixed64' },
  { no: 6, name: 'weights', type: 'fixed32', repeated: true, packed: true },
  { no: 7, name: 'verified', type: 'bool' },
];

function encodeItem(sku: string, qty: number, price: number): Uint8Array {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeString(sku);
  w.writeTag(2, WireType.Varint).writeUint32(qty);
  w.writeTag(3, WireType.Fixed64).writeDouble(price);
  return w.finish();
}

function encodeOrder(): Uint8Array {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeString('ORD-42');
  w.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(encodeItem('A-1', 3, 9.5));
  w.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(encodeItem('B-2', 1, -0.25));
  // packed int32: [1, -1, 300]
  const packed = new WireWriter().writeInt32(1).writeInt32(-1).writeInt32(300).finish();
  w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(packed);
  w.writeTag(4, WireType.Varint).writeSint64(-(1n << 40n));
  w.writeTag(5, WireType.Fixed64).writeFixed64(0xdeadbeefcafebaben);
  const weights = new WireWriter().writeFixed32(10).writeFixed32(20).finish();
  w.writeTag(6, WireType.LengthDelimited).writeLengthDelimited(weights);
  w.writeTag(7, WireType.Varint).writeBool(true);
  return w.finish();
}

describe('reflection schema engine', () => {
  it('decodes a multi-level nested message with packed repeated fields', () => {
    const decoded = decodeMessage(OrderSchema, encodeOrder());
    assert.deepEqual(decoded, {
      orderId: 'ORD-42',
      items: [
        { sku: 'A-1', qty: 3, price: 9.5 },
        { sku: 'B-2', qty: 1, price: -0.25 },
      ],
      tags: [1, -1, 300],
      delta: -(1n << 40n),
      checksum: 0xdeadbeefcafebaben,
      weights: [10, 20],
      verified: true,
    });
  });

  it('accepts mixed packed and unpacked repeated encodings', () => {
    const w = new WireWriter();
    const packed = new WireWriter().writeInt32(1).writeInt32(2).finish();
    w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(packed);
    w.writeTag(3, WireType.Varint).writeInt32(3); // unpacked occurrence
    const decoded = decodeMessage(OrderSchema, w.finish());
    assert.deepEqual(decoded.tags, [1, 2, 3]);
  });

  it('materializes absent repeated fields as empty arrays', () => {
    const decoded = decodeMessage(OrderSchema, new Uint8Array(0));
    assert.deepEqual(decoded.items, []);
    assert.deepEqual(decoded.tags, []);
    assert.equal(decoded.orderId, undefined);
  });

  it('skips unknown fields of every wire type', () => {
    const w = new WireWriter();
    w.writeTag(100, WireType.Varint).writeVarint64(999n);
    w.writeTag(101, WireType.Fixed64).writeFixed64(1n);
    w.writeTag(102, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([1, 2, 3]));
    w.writeTag(103, WireType.Fixed32).writeFixed32(4);
    w.writeTag(1, WireType.LengthDelimited).writeString('X');
    const decoded = decodeMessage(OrderSchema, w.finish());
    assert.equal(decoded.orderId, 'X');
  });

  it('reports wire-type mismatches with the field path', () => {
    const w = new WireWriter();
    w.writeTag(1, WireType.Fixed32).writeFixed32(1); // orderId expects LD
    assert.throws(
      () => decodeMessage(OrderSchema, w.finish()),
      (err: unknown) => err instanceof SchemaError && (err as SchemaError).path === 'orderId',
    );
  });

  it('reports nested mismatches with a dotted path', () => {
    const badItem = new WireWriter().writeTag(2, WireType.Fixed32).writeFixed32(1).finish();
    const w = new WireWriter();
    w.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(badItem);
    assert.throws(
      () => decodeMessage(OrderSchema, w.finish()),
      (err: unknown) => err instanceof SchemaError && (err as SchemaError).path === 'items.qty',
    );
  });

  it('requires a nested schema for message fields', () => {
    const broken: Schema = [{ no: 1, name: 'sub', type: 'message' }];
    const w = new WireWriter();
    w.writeTag(1, WireType.LengthDelimited).writeLengthDelimited(new Uint8Array(0));
    assert.throws(() => decodeMessage(broken, w.finish()), /missing nested schema/);
  });

  it('raises DecodeError on every truncation point of a nested stream', () => {
    const full = encodeOrder();
    let checked = 0;
    for (let cut = 0; cut < full.length; cut++) {
      const truncated = full.subarray(0, cut);
      try {
        decodeMessage(OrderSchema, truncated);
      } catch (err) {
        assert.ok(err instanceof DecodeError, `cut ${cut}: expected DecodeError, got ${err}`);
        checked++;
        continue;
      }
      // A cut that lands exactly between two complete top-level fields is a
      // valid shorter message; anything else must have thrown.
      const reader = new WireReader(full);
      let boundary = cut === 0; // empty prefix is a valid (empty) message
      while (!reader.done && !boundary) {
        const tag = reader.readTag();
        if (tag === null) break;
        reader.skipField(tag.wireType);
        if (reader.offset === cut) boundary = true;
      }
      assert.ok(boundary, `cut ${cut} silently accepted mid-field truncation`);
    }
    assert.ok(checked > 0, 'expected at least one truncation to be caught');
  });

  it('rejects a packed field truncated mid-payload', () => {
    const packed = new WireWriter().writeInt32(1).writeInt32(2).writeInt32(3).finish();
    const w = new WireWriter();
    w.writeTag(3, WireType.LengthDelimited).writeVarint32(packed.length + 5);
    w.writeBytes(packed);
    assert.throws(() => decodeMessage(OrderSchema, w.finish()), DecodeError);
  });

  it('rejects a packed varint run ending mid-varint', () => {
    const w = new WireWriter();
    w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(Uint8Array.from([0x01, 0x80]));
    assert.throws(() => decodeMessage(OrderSchema, w.finish()), /truncated varint/);
  });
});
