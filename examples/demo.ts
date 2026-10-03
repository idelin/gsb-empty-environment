/**
 * Runnable demo:
 *   1. hand-encode a nested protobuf stream, decode it via the schema engine
 *   2. demonstrate truncation safety
 *   3. run a saga whose last step fails -> reverse compensation kicks in
 *
 * Run with: node examples/demo.ts   (Node >= 22.6, or >= 23.6 for flagless TS)
 */

import { WireReader, WireType, WireWriter, DecodeError } from '../src/wire.ts';
import { decodeMessage, type Schema } from '../src/schema.ts';
import { SagaOrchestrator, type SagaStep } from '../src/saga.ts';

/* ---------------- 1. protobuf round-trip ---------------- */

const ItemSchema: Schema = [
  { no: 1, name: 'sku', type: 'string' },
  { no: 2, name: 'qty', type: 'uint32' },
  { no: 3, name: 'price', type: 'double' },
];

const OrderSchema: Schema = [
  { no: 1, name: 'orderId', type: 'string' },
  { no: 2, name: 'items', type: 'message', repeated: true, schema: ItemSchema },
  { no: 3, name: 'tags', type: 'sint32', repeated: true, packed: true },
  { no: 4, name: 'balanceDelta', type: 'sint64' },
  { no: 5, name: 'checksum', type: 'fixed64' },
];

function encodeItem(sku: string, qty: number, price: number): Uint8Array {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeString(sku);
  w.writeTag(2, WireType.Varint).writeUint32(qty);
  w.writeTag(3, WireType.Fixed64).writeDouble(price);
  return w.finish();
}

const w = new WireWriter();
w.writeTag(1, WireType.LengthDelimited).writeString('ORD-2026-0001');
w.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(encodeItem('SKU-apple', 3, 1.5));
w.writeTag(2, WireType.LengthDelimited).writeLengthDelimited(encodeItem('SKU-pear', 1, 2.75));
const packedTags = new WireWriter().writeSint32(-7).writeSint32(0).writeSint32(42).finish();
w.writeTag(3, WireType.LengthDelimited).writeLengthDelimited(packedTags);
w.writeTag(4, WireType.Varint).writeSint64(-9_000_000_000n);
w.writeTag(5, WireType.Fixed64).writeFixed64(0x0123456789abcdefn);
const stream = w.finish();

console.log('=== protobuf wire format ===');
console.log(`encoded ${stream.length} bytes:`,
  [...stream].map((b) => b.toString(16).padStart(2, '0')).join(' '));
const decoded = decodeMessage(OrderSchema, stream);
console.log('decoded:', JSON.stringify(decoded,
  (_k, v) => (typeof v === 'bigint' ? `${v}n` : v), 2));

/* ---------------- 2. truncation safety ---------------- */

console.log('\n=== truncation guard ===');
const truncated = stream.subarray(0, stream.length - 3);
try {
  decodeMessage(OrderSchema, truncated);
  console.log('unexpected: truncated stream decoded');
} catch (err) {
  console.log(`caught ${(err as DecodeError).name}: ${(err as Error).message}`);
}

/* ---------------- 3. saga reverse compensation ---------------- */

console.log('\n=== saga rollback ===');
interface ShopCtx {
  inventory: number;
  balance: number;
  shipments: string[];
}

const shop: ShopCtx = { inventory: 10, balance: 500, shipments: [] };

const reserveInventory: SagaStep<ShopCtx, number> = {
  name: 'reserve-inventory',
  execute(ctx) { ctx.inventory -= 3; console.log('  execute  reserve-inventory  inventory=10->7'); return 3; },
  compensate(ctx, qty) { ctx.inventory += qty; console.log('  compensate reserve-inventory  inventory=7->10'); },
};
const chargePayment: SagaStep<ShopCtx, number> = {
  name: 'charge-payment',
  execute(ctx) { ctx.balance -= 120; console.log('  execute  charge-payment     balance=500->380'); return 120; },
  compensate(ctx, amount) { ctx.balance += amount; console.log('  compensate charge-payment     balance=380->500'); },
};
const createShipment: SagaStep<ShopCtx, string> = {
  name: 'create-shipment',
  execute() { throw new Error('carrier API unavailable'); },
  compensate() {},
};

const orchestrator = new SagaOrchestrator<ShopCtx>();
try {
  await orchestrator.run('order-0001', [reserveInventory, chargePayment, createShipment], shop);
} catch (err) {
  console.log(`  tx failed: ${(err as Error).message}`);
}
const snap = orchestrator.snapshot('order-0001')!;
console.log(`  final state: ${snap.state}`);
console.log(`  shop after rollback: inventory=${shop.inventory} balance=${shop.balance}`);
orchestrator.assertNoLeaks();
console.log('  assertNoLeaks: OK');

/* keep WireReader referenced for readers of this file */
void WireReader;
