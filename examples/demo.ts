/**
 * End-to-end demo:
 *  1. hand-encode a nested OrderBook (packed repeated fills, fixed64, zigzag)
 *  2. decode it back with the zero-dependency wire parser
 *  3. run the reflection-mode inspector (dangling refs / orphan cycles / skew)
 *  4. truncate the stream and show the safe out-of-bounds guard
 */
import { encodeStruct, decodeStruct, createStats } from '../src/schema.ts';
import { Inspector } from '../src/inspector.ts';
import { TruncationError } from '../src/wire.ts';
import {
  OrderBookSchema,
  OrderSchema,
  UserSchema,
  sampleBook,
} from '../test/helpers/orderbook.ts';

const json = (v: unknown): string =>
  JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? `${x}n` : x), 2);

const hex = (b: Uint8Array): string =>
  [...b].map((x) => x.toString(16).padStart(2, '0')).join(' ');

console.log('=== 1. Encode nested OrderBook to protobuf wire format ===');
const book = sampleBook();
const bytes = encodeStruct(OrderBookSchema, book);
console.log(`encoded ${bytes.length} bytes:`);
console.log(hex(bytes));

console.log('\n=== 2. Decode back to struct (hand-rolled parser) ===');
const stats = createStats();
const decoded = decodeStruct(OrderBookSchema, bytes, stats);
console.log(json(decoded));
console.log(`unknown fields: ${stats.unknownFields.length}, leaks: ${stats.leaks.length}`);

const baseInspector = (): Inspector =>
  new Inspector()
    .registerSchema(OrderBookSchema)
    .registerSchema(OrderSchema)
    .registerSchema(UserSchema);

console.log('\n=== 3a. Inspect clean book ===');
console.log(json(baseInspector().inspect('OrderBook', decoded, { leaks: stats.leaks })));

console.log('\n=== 3b. Inspect tampered book (dangling ref + orphan cycle + skew + invariant) ===');
const inspector = baseInspector().addInvariant('non-negative-qty', (ctx) => {
  for (const inst of ctx.instances) {
    if (inst.schema === 'Order' && (inst.value.qty as bigint) < 0n) {
      return `order ${inst.value.order_id} at ${inst.path} has negative qty`;
    }
  }
  return null;
});
const tampered = structuredClone(decoded);
(tampered.orders as Record<string, unknown>[])[0].user_id = 999n; // dangling ref
tampered.archived = [
  { order_id: 201n, symbol: 'X', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 202n },
  { order_id: 202n, symbol: 'Y', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 201n },
];
(tampered.orders as Record<string, unknown>[])[1].fills = Array.from({ length: 60 }, (_, i) =>
  BigInt(i),
); // skew
const report = inspector.inspect('OrderBook', tampered);
for (const v of report.violations) {
  console.log(`  [${v.kind}] ${v.path}: ${v.message}`);
}
console.log(`ok=${report.ok} nodes=${report.nodes} edges=${report.edges}`);

console.log('\n=== 4. Truncated stream -> safe out-of-bounds guard ===');
const cut = bytes.subarray(0, bytes.length - 3);
try {
  decodeStruct(OrderBookSchema, cut, createStats());
  console.log('unexpectedly decoded');
} catch (err) {
  if (err instanceof TruncationError) {
    console.log(`TruncationError: ${err.message}`);
    console.log(`guard: offset=${err.offset} needed=${err.needed}B available=${err.available}B`);
  } else {
    throw err;
  }
}
