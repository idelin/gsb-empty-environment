import test from 'node:test';
import assert from 'node:assert/strict';
import { OptimisticStore, type Operation } from '../src/store.ts';

type V = Record<string, unknown>;

function op(id: string, kind: Operation<V>['kind'], target: string, value?: V): Operation<V> {
  return value === undefined ? { id, kind, target } : { id, kind, target, value };
}

function asObject(map: ReadonlyMap<string, V>): Record<string, V> {
  return Object.fromEntries(map);
}

test('out-of-order accepts commit in submission order', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.submit(op('b', 'update', 'x', { n: 2 }));
  store.submit(op('c', 'create', 'y', { n: 3 }));

  // Receipts arrive completely out of order: c, b, a.
  store.receiveReceipt('c', true);
  store.receiveReceipt('b', true);
  // Nothing committed yet: head 'a' is unresolved.
  assert.equal(store.baseline.size, 0);
  assert.deepEqual(asObject(store.state()), { x: { n: 2 }, y: { n: 3 } });

  store.receiveReceipt('a', true);
  // 'a' commits, then the buffered accepts for 'b' and 'c' drain in order.
  assert.deepEqual(asObject(store.baseline), { x: { n: 2 }, y: { n: 3 } });
  assert.equal(store.pending.length, 0);
  assert.equal(store.statusOf('a'), 'committed');
  assert.equal(store.statusOf('b'), 'committed');
  assert.equal(store.statusOf('c'), 'committed');
});

test('rejecting one op removes only it and replays successors', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.submit(op('b', 'update', 'x', { n: 2, extra: true }));
  store.submit(op('c', 'create', 'y', { n: 3 }));

  store.receiveReceipt('b', false);
  assert.equal(store.statusOf('b'), 'rejected');
  // 'b' is gone; 'a' and 'c' still replay. No snapshot restore happened.
  assert.deepEqual(asObject(store.state()), { x: { n: 1 }, y: { n: 3 } });

  store.receiveReceipt('c', true);
  store.receiveReceipt('a', true);
  assert.deepEqual(asObject(store.baseline), { x: { n: 1 }, y: { n: 3 } });
});

test('rejection does not clobber already-committed baseline ops', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.receiveReceipt('a', true); // committed into baseline
  store.submit(op('b', 'update', 'x', { n: 2 }));
  store.submit(op('c', 'create', 'y', { n: 9 }));

  store.receiveReceipt('b', false);
  // Committed 'a' survives; optimistic 'c' survives; only 'b' is gone.
  assert.deepEqual(asObject(store.state()), { x: { n: 1 }, y: { n: 9 } });
  assert.deepEqual(asObject(store.baseline), { x: { n: 1 } });
});

test('successors referencing a vanished object are dependency-failed', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.submit(op('b', 'update', 'x', { n: 2 }));
  store.submit(op('c', 'delete', 'x'));
  store.submit(op('d', 'create', 'y', { n: 7 }));

  store.receiveReceipt('a', false);
  assert.equal(store.statusOf('a'), 'rejected');
  // 'b' and 'c' both needed the 'x' that 'a' would have created.
  assert.equal(store.statusOf('b'), 'dependency-failed');
  assert.equal(store.statusOf('c'), 'dependency-failed');
  // Unrelated op 'd' is untouched.
  assert.equal(store.statusOf('d'), 'pending');
  assert.deepEqual(asObject(store.state()), { y: { n: 7 } });
});

test('out-of-order reject takes effect before earlier receipts arrive', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.submit(op('b', 'create', 'y', { n: 2 }));
  store.submit(op('c', 'update', 'y', { n: 3 }));

  // Reject for 'b' arrives first, before any receipt for 'a'.
  store.receiveReceipt('b', false);
  assert.equal(store.statusOf('c'), 'dependency-failed');
  assert.equal(store.statusOf('a'), 'pending');
  assert.deepEqual(asObject(store.state()), { x: { n: 1 } });

  store.receiveReceipt('a', true);
  assert.deepEqual(asObject(store.baseline), { x: { n: 1 } });
});

test('rejected delete keeps the object; dependent recreate fails', () => {
  const store = new OptimisticStore<V>([['x', { n: 1 }]]);
  store.submit(op('a', 'delete', 'x'));
  store.submit(op('b', 'create', 'x', { n: 2 }));

  store.receiveReceipt('a', false);
  // 'x' was never deleted, so recreating it is invalid.
  assert.equal(store.statusOf('b'), 'dependency-failed');
  assert.deepEqual(asObject(store.state()), { x: { n: 1 } });
});

test('duplicate and unknown receipts are ignored', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));

  assert.equal(store.receiveReceipt('nope', true), false);
  assert.equal(store.receiveReceipt('a', true), true);
  assert.equal(store.receiveReceipt('a', true), false); // duplicate
  assert.equal(store.receiveReceipt('a', false), false); // contradictory late reject
  assert.equal(store.statusOf('a'), 'committed');
  assert.deepEqual(asObject(store.baseline), { x: { n: 1 } });
});

test('dependency-failed ops still accept their own receipts', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  store.submit(op('b', 'update', 'x', { n: 2 }));

  store.receiveReceipt('a', false);
  assert.equal(store.statusOf('b'), 'dependency-failed');
  // The server's own (negative) receipt for 'b' resolves it terminally.
  assert.equal(store.receiveReceipt('b', false), true);
  assert.equal(store.statusOf('b'), 'rejected');
  assert.equal(store.pending.length, 0);
});

test('dependency failure recovers when the destroying op is rejected', () => {
  // Regression: 'a' deletes x optimistically, making 'b' fail. The server
  // rejects 'a' but accepts 'b' — 'b' must come back and commit, even
  // though its accept arrived while it was still marked failed.
  const store = new OptimisticStore<V>([['x', { n: 1 }]]);
  store.submit(op('a', 'delete', 'x'));
  store.submit(op('b', 'update', 'x', { n: 2 }));
  assert.equal(store.statusOf('b'), 'dependency-failed');
  assert.deepEqual(asObject(store.state()), {});

  // Out of order: 'b' accepted first, then 'a' rejected.
  store.receiveReceipt('b', true);
  assert.equal(store.statusOf('b'), 'dependency-failed'); // still blocked by 'a'
  store.receiveReceipt('a', false);
  assert.equal(store.statusOf('b'), 'committed');
  assert.deepEqual(asObject(store.baseline), { x: { n: 2 } });
  assert.deepEqual(asObject(store.state()), { x: { n: 2 } });
});

test('duplicate submit and missing value are rejected', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1 }));
  assert.throws(() => store.submit(op('a', 'create', 'z', { n: 1 })), /duplicate/);
  assert.throws(() => store.submit(op('b', 'create', 'z')), /requires a value/);
});

test('update merges plain objects, replaces other values', () => {
  const store = new OptimisticStore<V>();
  store.submit(op('a', 'create', 'x', { n: 1, keep: 'yes' }));
  store.submit(op('b', 'update', 'x', { n: 2 }));
  assert.deepEqual(asObject(store.state()), { x: { n: 2, keep: 'yes' } });
});
