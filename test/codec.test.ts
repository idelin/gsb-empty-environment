import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireReader } from '../src/reader.ts';
import { WireWriter } from '../src/writer.ts';
import { WireType, TruncationError, WireError } from '../src/wire.ts';
import { decodeStruct, encodeStruct, createStats } from '../src/schema.ts';
import { OrderBookSchema, OrderSchema, sampleBook } from './helpers/orderbook.ts';

test('nested struct with packed repeated arrays round-trips exactly', () => {
  const book = sampleBook();
  const bytes = encodeStruct(OrderBookSchema, book);
  const stats = createStats();
  const decoded = decodeStruct(OrderBookSchema, bytes, stats);
  assert.deepEqual(decoded, book);
  assert.equal(stats.unknownFields.length, 0);
  assert.equal(stats.leaks.length, 0, 'strictly no leaks');
});

test('packed repeated field emits a single LEN record', () => {
  const bytes = encodeStruct(OrderSchema, {
    order_id: 7n,
    fills: [1n, 2n, 300n, 16384n],
  });
  // tag(6,LEN)=0x32, len=... then payload
  assert.equal(bytes[2], 0x32);
  const stats = createStats();
  const decoded = decodeStruct(OrderSchema, bytes, stats);
  assert.deepEqual(decoded.fills, [1n, 2n, 300n, 16384n]);
});

test('decoder accepts mixed packed and unpacked occurrences (proto3 rule)', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(9n); // order_id
  w.writeTag(6, WireType.Varint).writeVarint(5n); // unpacked fill
  const packed = new WireWriter();
  packed.writeVarint(6n).writeVarint(7n);
  w.writeTag(6, WireType.Len).writeBytes(packed.toBytes()); // packed fills
  w.writeTag(6, WireType.Varint).writeVarint(8n); // unpacked again
  const decoded = decodeStruct(OrderSchema, w.toBytes());
  assert.deepEqual(decoded.fills, [5n, 6n, 7n, 8n]);
});

test('unknown fields are skipped safely and recorded', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(42n); // known: order_id
  w.writeTag(15, WireType.I32).writeFixed32(0xdeadbeef); // unknown fixed32
  w.writeTag(16, WireType.Len).writeString('mystery'); // unknown len
  w.writeTag(17, WireType.I64).writeFixed64(0xcafebaben); // unknown fixed64
  w.writeTag(18, WireType.Varint).writeVarint(123456789n); // unknown varint
  const stats = createStats();
  const decoded = decodeStruct(OrderSchema, w.toBytes(), stats);
  assert.equal(decoded.order_id, 42n);
  assert.equal(stats.unknownFields.length, 4);
  assert.deepEqual(
    stats.unknownFields.map((u) => u.field),
    [15, 16, 17, 18],
  );
});

test('every truncation point of a nested stream raises TruncationError, never reads OOB', () => {
  const bytes = encodeStruct(OrderBookSchema, sampleBook());
  let truncations = 0;
  for (let cut = 0; cut < bytes.length; cut += 1) {
    const slice = bytes.subarray(0, cut);
    try {
      decodeStruct(OrderBookSchema, slice, createStats());
    } catch (err) {
      assert.ok(err instanceof TruncationError, `cut=${cut} threw ${err}`);
      assert.ok(err.offset <= cut, `offset ${err.offset} beyond cut ${cut}`);
      truncations += 1;
    }
  }
  assert.ok(truncations > 0, 'expected truncation guards to fire');
});

test('length prefix larger than the remaining buffer is rejected safely', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Len).writeVarint(1000); // claims 1000B, provides 1
  w.writeBytes(Uint8Array.of(0x08));
  assert.throws(
    () => decodeStruct(OrderBookSchema, w.toBytes()),
    (err: unknown) => {
      assert.ok(err instanceof TruncationError);
      assert.equal(err.needed, 1000);
      return true;
    },
  );
});

test('oversized length prefix (> 2^31-1) is rejected as a wire error', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Len).writeVarint(0x80000000n);
  assert.throws(() => decodeStruct(OrderBookSchema, w.toBytes()), WireError);
});

test('wire type mismatch on a known field is a hard error', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Varint).writeVarint(1n); // symbol declared as string(LEN)
  assert.throws(() => decodeStruct(OrderSchema, w.toBytes()), /wire type mismatch/);
});

test('field number 0 and unsupported wire types are rejected', () => {
  assert.throws(() => decodeStruct(OrderSchema, Uint8Array.of(0x00)), /field number 0/);
  const w = new WireWriter();
  w.writeTag(9, 7); // wire type 7 does not exist
  assert.throws(() => decodeStruct(OrderSchema, w.toBytes()), /unsupported wire type/);
});

test('reader leak assertion: fully drained streams report no leaks', () => {
  const bytes = encodeStruct(OrderBookSchema, sampleBook());
  const reader = new WireReader(bytes);
  decodeStruct(OrderBookSchema, reader, createStats());
  assert.deepEqual(reader.assertDrained(), []);
});

test('reader leak assertion: undrained sub-readers are reported', () => {
  const bytes = encodeStruct(OrderSchema, { order_id: 1n, symbol: 'ABC' });
  const reader = new WireReader(bytes);
  reader.readTag();
  reader.readVarint(); // consume order_id only; leave the rest
  const leaks = reader.assertDrained();
  assert.equal(leaks.length, 1);
  assert.match(leaks[0], /unconsumed/);
});

test('multi-level nested messages with packed arrays decode exactly', () => {
  const order = (id: bigint, fills: bigint[]): Record<string, unknown> => ({
    order_id: id,
    symbol: 'X',
    price: 1n,
    qty: 1n,
    user_id: 1n,
    fills,
  });
  const inner: Record<string, unknown> = {
    users: [{ user_id: 1n, name: 'u', tier: 1n }],
    orders: [order(1n, [1n]), order(2n, [2n, 3n]), order(3n, []), order(4n, [9n])],
    archived: [order(5n, [1n, 1n, 1n])],
    venue: 'deep',
  };
  const bytes = encodeStruct(OrderBookSchema, inner);
  const decoded = decodeStruct(OrderBookSchema, bytes, createStats());
  assert.deepEqual(decoded, inner);
});
