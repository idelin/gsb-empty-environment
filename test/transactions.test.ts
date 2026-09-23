import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyActions, createStore } from '../src/store.ts';
import { fromState } from '../src/selector.ts';

test('multiple mutations in one transaction publish only the final diff', () => {
  const store = createStore({ count: 0, label: 'init' });
  const count = fromState((s: { count: number }) => s.count);
  const seen: number[] = [];
  store.subscribe(count, (v) => seen.push(v));

  store.transaction((tx) => {
    tx.set(['count'], 1);
    tx.set(['count'], 2);
    tx.update<number>(['count'], (c) => c + 1);
    tx.set(['label'], 'done');
  });

  assert.deepEqual(seen, [3]); // single notification, final value only
  assert.equal(store.stats.notifications, 1);
  assert.equal(store.stats.commits, 1);
});

test('readers never see intermediate transaction state', () => {
  const store = createStore({ a: 0, b: 0 });
  const selA = fromState((s: { a: number }) => s.a);
  store.subscribe(selA, () => {});

  const observed: Array<[number, number]> = [];
  store.transaction((tx) => {
    tx.set(['a'], 1);
    observed.push([store.getState().a, store.get(selA)]);
    tx.set(['a'], 2);
    observed.push([store.getState().a, store.get(selA)]);
  });

  assert.deepEqual(observed, [
    [0, 0],
    [0, 0],
  ]);
  assert.equal(store.getState().a, 2);
  assert.equal(store.get(selA), 2);
});

test('replacing the root with an equal projection does not notify', () => {
  const store = createStore({ a: 1, nested: { b: [1, 2] } });
  const selA = fromState((s: { a: number }) => s.a);
  const selNested = fromState((s: { nested: { b: number[] } }) => s.nested);
  const seenA: number[] = [];
  const seenN: Array<{ b: number[] }> = [];
  store.subscribe(selA, (v) => seenA.push(v));
  store.subscribe(selNested, (v) => seenN.push(v));

  const commitsBefore = store.stats.commits;
  store.replace(structuredClone(store.getState())); // new references, equal content
  assert.deepEqual(seenA, []);
  assert.deepEqual(seenN, []);
  assert.equal(store.stats.commits, commitsBefore);

  // New root where only an unrelated branch differs: projection still equal.
  store.replace({ a: 1, nested: { b: [1, 2] } });
  assert.deepEqual(seenA, []);
  assert.deepEqual(seenN, []);
});

test('state operations are replayable from the action log', () => {
  const initial = { n: 0, items: [] as string[] };
  const store = createStore(structuredClone(initial));
  store.set(['n'], 5);
  store.update<string[]>(['items'], (items) => [...items, 'a']);
  store.transaction((tx) => {
    tx.set(['n'], 7);
    tx.update<string[]>(['items'], (items) => [...items, 'b']);
  });

  const replayed = applyActions(initial, store.actionLog);
  assert.deepEqual(replayed, store.getState());

  const before = store.getState();
  store.replay(); // reset to initial state and re-apply the same log
  assert.deepEqual(store.getState(), before);
});

test('freeze option protects mutable projections', () => {
  const store = createStore({ list: [1, 2] }, { freeze: true });
  const list = fromState((s: { list: number[] }) => s.list);
  const derived: number[][] = [];
  store.subscribe(list, (v) => derived.push(v));
  store.set(['list'], [1, 2, 3]);

  assert.equal(derived.length, 1);
  assert.throws(() => {
    derived[0].push(4);
  }, TypeError);
  assert.throws(() => {
    (store.getState().list as number[]).pop();
  }, TypeError);
});

test('snapshot option delivers an isolated copy to listeners', () => {
  const store = createStore({ obj: { v: 1 } });
  const obj = fromState((s: { obj: { v: number } }) => s.obj);
  store.subscribe(
    obj,
    (v) => {
      v.v = 999; // mutating the snapshot must not touch shared state
    },
    { snapshot: true },
  );
  store.set(['obj'], { v: 2 });
  assert.equal(store.getState().obj.v, 2);
});
