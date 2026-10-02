import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Inspector } from '../src/inspector.ts';
import { encodeStruct, decodeStruct, createStats } from '../src/schema.ts';
import {
  OrderBookSchema,
  OrderSchema,
  UserSchema,
  sampleBook,
} from './helpers/orderbook.ts';

function makeInspector(): Inspector {
  return new Inspector()
    .registerSchema(OrderBookSchema)
    .registerSchema(OrderSchema)
    .registerSchema(UserSchema);
}

test('clean book passes full inspection with zero violations', () => {
  const report = makeInspector().inspect('OrderBook', sampleBook());
  assert.equal(report.ok, true);
  assert.deepEqual(report.violations, []);
  assert.equal(report.nodes, 5); // book + 2 users + 2 orders
  assert.equal(report.edges, 3); // 2x user_id + 1x next
  assert.equal(report.unreachable, 0);
});

test('dangling reference to a missing user is reported', () => {
  const book = sampleBook();
  (book.orders as Record<string, unknown>[])[0].user_id = 999n;
  const report = makeInspector().inspect('OrderBook', book);
  assert.equal(report.ok, false);
  const dangling = report.violations.filter((v) => v.kind === 'dangling-ref');
  assert.equal(dangling.length, 1);
  assert.match(dangling[0].message, /User#999/);
  assert.match(dangling[0].path, /orders\[0\]/);
});

test('dangling reference in a repeated ref field reports each miss', () => {
  const book = sampleBook();
  const orders = book.orders as Record<string, unknown>[];
  orders[0].next = 5555n;
  orders[1].next = 6666n;
  const report = makeInspector().inspect('OrderBook', book);
  assert.equal(report.violations.filter((v) => v.kind === 'dangling-ref').length, 2);
});

test('orphan cycle among archived orders is detected as a loop', () => {
  const book = sampleBook();
  book.archived = [
    { order_id: 201n, symbol: 'X', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 202n },
    { order_id: 202n, symbol: 'Y', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 201n },
  ];
  const report = makeInspector().inspect('OrderBook', book);
  const cycles = report.violations.filter((v) => v.kind === 'orphan-cycle');
  assert.equal(cycles.length, 1);
  assert.match(cycles[0].message, /archived\[0\]/);
  assert.match(cycles[0].message, /archived\[1\]/);
  // user_id refs from the orphans still resolve (users are root-anchored)
  assert.equal(report.violations.filter((v) => v.kind === 'dangling-ref').length, 0);
});

test('self-referencing archived order is a 1-node orphan cycle', () => {
  const book = sampleBook();
  book.archived = [
    { order_id: 301n, symbol: 'Z', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 301n },
  ];
  const report = makeInspector().inspect('OrderBook', book);
  const cycles = report.violations.filter((v) => v.kind === 'orphan-cycle');
  assert.equal(cycles.length, 1);
  assert.match(cycles[0].message, /1 node/);
});

test('unreachable acyclic archived order is an orphan node, not a cycle', () => {
  const book = sampleBook();
  book.archived = [
    { order_id: 401n, symbol: 'W', price: 1n, qty: 1n, user_id: 1n, fills: [] },
  ];
  const report = makeInspector().inspect('OrderBook', book);
  assert.equal(report.violations.filter((v) => v.kind === 'orphan-cycle').length, 0);
  const orphans = report.violations.filter((v) => v.kind === 'orphan-node');
  assert.equal(orphans.length, 1);
  assert.match(orphans[0].path, /archived\[0\]/);
});

test('archived node referenced from a live order is reachable (no orphan)', () => {
  const book = sampleBook();
  (book.orders as Record<string, unknown>[])[1].next = 501n;
  book.archived = [
    { order_id: 501n, symbol: 'V', price: 1n, qty: 1n, user_id: 2n, fills: [] },
  ];
  const report = makeInspector().inspect('OrderBook', book);
  assert.equal(report.violations.filter((v) => v.kind.startsWith('orphan')).length, 0);
});

test('data skew on packed fills cardinality is quantified and flagged', () => {
  const book = sampleBook();
  book.orders = [
    { order_id: 1n, symbol: 'A', price: 1n, qty: 1n, user_id: 1n, fills: [1n] },
    { order_id: 2n, symbol: 'B', price: 1n, qty: 1n, user_id: 1n, fills: [1n] },
    { order_id: 3n, symbol: 'C', price: 1n, qty: 1n, user_id: 1n, fills: [1n] },
    {
      order_id: 4n,
      symbol: 'D',
      price: 1n,
      qty: 1n,
      user_id: 1n,
      fills: Array.from({ length: 97 }, (_, i) => BigInt(i)),
    },
  ];
  const report = makeInspector().inspect('OrderBook', book, { skewThreshold: 3 });
  const skew = report.violations.filter((v) => v.kind === 'data-skew');
  assert.equal(skew.length, 1);
  assert.match(skew[0].message, /Order\.fills/);
  const stat = report.skew.find((s) => s.field === 'Order.fills');
  assert.ok(stat);
  assert.equal(stat.max, 97);
  assert.equal(stat.mean, 25);
  assert.ok(stat.ratio >= 3);
});

test('uniform cardinalities stay below the skew threshold', () => {
  const report = makeInspector().inspect('OrderBook', sampleBook(), { skewThreshold: 3 });
  assert.equal(report.violations.filter((v) => v.kind === 'data-skew').length, 0);
});

test('custom formal invariants are evaluated over the whole tree', () => {
  const inspector = makeInspector().addInvariant('non-negative-qty', (ctx) => {
    for (const inst of ctx.instances) {
      if (inst.schema === 'Order' && (inst.value.qty as bigint) < 0n) {
        return `order ${inst.value.order_id} at ${inst.path} has negative qty`;
      }
    }
    return null;
  });
  const bad = inspector.inspect('OrderBook', sampleBook());
  assert.equal(bad.ok, false);
  assert.match(bad.violations.find((v) => v.kind === 'invariant')?.message ?? '', /negative qty/);

  const good = structuredClone(sampleBook());
  (good.orders as Record<string, unknown>[])[1].qty = 8n;
  assert.equal(inspector.inspect('OrderBook', good).ok, true);
});

test('duplicate entity keys violate the uniqueness invariant', () => {
  const book = sampleBook();
  (book.users as Record<string, unknown>[])[1].user_id = 1n;
  const report = makeInspector().inspect('OrderBook', book);
  const dup = report.violations.filter((v) => v.kind === 'invariant' && /duplicate key/.test(v.message));
  assert.equal(dup.length, 1);
});

test('decoder-reported leaks surface as leak violations', () => {
  const report = makeInspector().inspect('OrderBook', sampleBook(), {
    leaks: [{ path: '$.orders[0]', unconsumed: 7 }],
  });
  const leaks = report.violations.filter((v) => v.kind === 'leak');
  assert.equal(leaks.length, 1);
  assert.match(leaks[0].message, /7B/);
});

test('inspection is deterministic across repeated runs (no shared state)', () => {
  const book = sampleBook();
  book.archived = [
    { order_id: 201n, symbol: 'X', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 202n },
    { order_id: 202n, symbol: 'Y', price: 1n, qty: 1n, user_id: 1n, fills: [], next: 201n },
  ];
  const inspector = makeInspector();
  const first = JSON.stringify(inspector.inspect('OrderBook', book));
  for (let i = 0; i < 25; i += 1) {
    assert.equal(JSON.stringify(inspector.inspect('OrderBook', book)), first);
  }
});

test('end-to-end: encode -> decode -> inspect a tampered stream', () => {
  const book = sampleBook();
  (book.orders as Record<string, unknown>[])[0].user_id = 777n;
  const bytes = encodeStruct(OrderBookSchema, book);
  const stats = createStats();
  const decoded = decodeStruct(OrderBookSchema, bytes, stats);
  const report = makeInspector().inspect('OrderBook', decoded, { leaks: stats.leaks });
  assert.equal(report.ok, false);
  assert.equal(report.violations.filter((v) => v.kind === 'dangling-ref').length, 1);
  assert.equal(report.violations.filter((v) => v.kind === 'leak').length, 0);
});
