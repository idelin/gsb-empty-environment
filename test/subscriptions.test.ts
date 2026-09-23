import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createStore } from '../src/store.ts';
import { fromState } from '../src/selector.ts';

test('a throwing equality function does not break other subscribers', () => {
  const errors: unknown[] = [];
  const store = createStore(
    { bad: 0, good: 0 },
    { onEqualityError: (err) => { errors.push(err); } },
  );
  const bad = fromState((s: { bad: number }) => s.bad, {
    equality: () => {
      throw new Error('equality boom');
    },
  });
  const good = fromState((s: { good: number }) => s.good);

  const badSeen: number[] = [];
  const goodSeen: number[] = [];
  store.subscribe(bad, (v) => badSeen.push(v));
  store.subscribe(good, (v) => goodSeen.push(v));

  store.set(['bad'], 1);
  store.set(['good'], 1);

  assert.deepEqual(badSeen, [1]); // default policy: treat as changed, still notify
  assert.deepEqual(goodSeen, [1]); // other subscribers unaffected
  assert.equal(errors.length >= 1, true);
  assert.equal(store.stats.equalityErrors >= 1, true);
  assert.match(String(errors[0]), /equality boom/);
});

test("equality error policy 'suppress' skips that notification only", () => {
  const store = createStore(
    { bad: 0, good: 0 },
    { onEqualityError: () => 'suppress' },
  );
  const bad = fromState((s: { bad: number }) => s.bad, {
    equality: () => {
      throw new Error('boom');
    },
  });
  const good = fromState((s: { good: number }) => s.good);
  const badSeen: number[] = [];
  const goodSeen: number[] = [];
  store.subscribe(bad, (v) => badSeen.push(v));
  store.subscribe(good, (v) => goodSeen.push(v));

  store.set(['bad'], 5);
  store.set(['good'], 5);
  assert.deepEqual(badSeen, []); // suppressed by policy
  assert.deepEqual(goodSeen, [5]);
});

test('reentrant writes from listeners commit as new transactions and stay consistent', () => {
  const store = createStore({ a: 0, b: 0 }, { verify: true });
  const selA = fromState((s: { a: number }) => s.a);
  const selB = fromState((s: { b: number }) => s.b);
  const events: Array<[string, number]> = [];

  store.subscribe(selA, (v) => {
    events.push(['a', v]);
    if (v === 1) store.set(['b'], 10); // reentrant write during dispatch
  });
  store.subscribe(selB, (v) => events.push(['b', v]));

  store.set(['a'], 1);

  assert.deepEqual(events, [
    ['a', 1],
    ['b', 10],
  ]);
  assert.equal(store.getState().b, 10);
  assert.equal(store.stats.notifications, 2);
  assert.equal(store.stats.commits, 2);
  assert.equal(store.stats.verificationFailures, 0); // independent full-projection check
  assert.deepEqual(store.verificationLog, []);
});

test('unsubscribing during dispatch drops queued notifications (cancellation interleaving)', () => {
  const store = createStore({ v: 0 }, { verify: true });
  const sel = fromState((s: { v: number }) => s.v);
  const seenA: number[] = [];
  const seenB: number[] = [];

  let unsubB: () => void = () => {};
  store.subscribe(sel, (v) => {
    seenA.push(v);
    unsubB(); // cancel B while its notification is still queued
  });
  unsubB = store.subscribe(sel, (v) => seenB.push(v));

  store.set(['v'], 1);
  assert.deepEqual(seenA, [1]);
  assert.deepEqual(seenB, []); // dropped, not delivered

  store.set(['v'], 2);
  assert.deepEqual(seenA, [1, 2]);
  assert.deepEqual(seenB, []);
  assert.equal(store.stats.verificationFailures, 0);
});

test('reentrant commit loop is bounded by maxFlushCommits', () => {
  const store = createStore({ n: 0 }, { maxFlushCommits: 8 });
  const sel = fromState((s: { n: number }) => s.n);
  store.subscribe(sel, (v) => {
    if (v < 1000) store.set(['n'], v + 1); // would loop forever without the guard
  });
  assert.throws(() => store.set(['n'], 1), /reentrant commit limit/);
});

test('independent verification catches nothing in normal operation and counts recomputes', () => {
  const store = createStore({ x: 1, y: 2 }, { verify: true });
  const sum = fromState((s: { x: number; y: number }) => s.x + s.y);
  const seen: number[] = [];
  store.subscribe(sum, (v) => seen.push(v));

  store.transaction((tx) => {
    tx.set(['x'], 10);
    tx.set(['y'], 20);
  });

  assert.deepEqual(seen, [30]);
  assert.equal(store.stats.verificationFailures, 0);
  assert.equal(store.stats.recomputes, 2); // initial + one recompute after commit
  assert.equal(store.stats.notifications, 1);
});
