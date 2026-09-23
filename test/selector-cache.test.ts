import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createStore } from '../src/store.ts';
import { createSelector, createSelectorFamily, fromState } from '../src/selector.ts';

test('subscribers only observe their selector projection', () => {
  const store = createStore({ a: 1, b: 10 });
  const selA = fromState((s: { a: number; b: number }) => s.a);
  const seen: number[] = [];
  store.subscribe(selA, (v) => seen.push(v));

  store.set(['b'], 99); // unrelated branch
  assert.deepEqual(seen, []);
  assert.equal(store.nodeStats(selA).recomputes, 1); // initial evaluation only

  store.set(['a'], 2);
  assert.deepEqual(seen, [2]);
  assert.equal(store.nodeStats(selA).recomputes, 2);
});

test('configurable equality suppresses no-change notifications', () => {
  const store = createStore({ user: { id: 1, name: 'ada', tick: 0 } });
  type User = { id: number; name: string; tick: number };
  const byId = fromState((s: { user: User }) => s.user, {
    equality: (a: User, b: User) => a.id === b.id && a.name === b.name,
    equalityVersion: 'user-v1',
  });
  const seen: User[] = [];
  store.subscribe(byId, (v) => seen.push(v));

  store.set(['user'], { id: 1, name: 'ada', tick: 1 }); // projection-equal
  assert.deepEqual(seen, []);
  assert.equal(store.stats.suppressed >= 1, true);

  store.set(['user'], { id: 1, name: 'grace', tick: 2 });
  assert.deepEqual(seen.map((u) => u.name), ['grace']);
});

test('parameterized caches are never mixed across parameters', () => {
  const store = createStore({ items: { x: 'X', y: 'Y' } });
  const family = createSelectorFamily((id: string) =>
    fromState((s: { items: Record<string, string> }) => s.items[id]),
  );
  const selX1 = family.for('x');
  const selX2 = family.for('x');
  const selY = family.for('y');

  assert.equal(selX1, selX2); // same identity -> shared cache slot
  assert.notEqual(selX1, selY);
  assert.equal(family.cacheSize(), 2);
  assert.equal(store.get(selX1), 'X');
  assert.equal(store.get(selY), 'Y');

  const seenX: string[] = [];
  const seenY: string[] = [];
  store.subscribe(selX1, (v) => seenX.push(v));
  store.subscribe(selY, (v) => seenY.push(v));
  store.set(['items', 'x'], 'X2');
  assert.deepEqual(seenX, ['X2']);
  assert.deepEqual(seenY, []); // y's cache untouched by x's change
});

test('family entries share the parameterized subgraph (inputs recompute once)', () => {
  const store = createStore({ users: { u1: { n: 'a' }, u2: { n: 'b' } }, noise: 0 });
  type State = { users: Record<string, { n: string }>; noise: number };
  const users = fromState((s: State) => s.users);
  const byId = createSelectorFamily((id: string) =>
    createSelector([users], (all: Record<string, { n: string }>) => all[id].n),
  );
  const n1 = byId.for('u1');
  const n2 = byId.for('u2');
  store.subscribe(n1, () => {});
  store.subscribe(n2, () => {});

  const before = store.nodeStats(users).recomputes;
  store.set(['users', 'u1', 'n'], 'a2');
  assert.equal(store.nodeStats(users).recomputes, before + 1); // shared input: one recompute
  assert.equal(store.get(n1), 'a2');
  assert.equal(store.get(n2), 'b');
});

test('cache identity = params + equality version + input dependencies', () => {
  const state = { items: { k: 1 }, alt: { k: 2 } };
  const store = createStore(state);

  let eqVersion = 'v1';
  const fam = createSelectorFamily((id: string) =>
    fromState((s: typeof state) => s.items[id], { equalityVersion: eqVersion }),
  );
  const first = fam.for('k');
  eqVersion = 'v2'; // same params, new equality version -> new identity
  const second = fam.for('k');
  assert.notEqual(first, second);
  assert.equal(fam.cacheSize(), 2);
  assert.ok(fam.cacheKeys().some((k) => k.includes('eq:v1')));
  assert.ok(fam.cacheKeys().some((k) => k.includes('eq:v2')));

  // Same params, different input dependencies -> different identity.
  const items = fromState((s: typeof state) => s.items);
  const alt = fromState((s: typeof state) => s.alt);
  let useAlt = false;
  const fam2 = createSelectorFamily((id: string) =>
    createSelector([useAlt ? alt : items], (m: Record<string, number>) => m[id]),
  );
  const base = fam2.for('k');
  useAlt = true;
  const switched = fam2.for('k');
  assert.notEqual(base, switched);
  assert.equal(fam2.cacheSize(), 2);
  assert.equal(store.get(base), 1);
  assert.equal(store.get(switched), 2);
});

test('branch switch revokes stale dependencies', () => {
  const store = createStore({ tab: 'a', a: { x: 1 }, b: { y: 2 } });
  type State = { tab: string; a: { x: number }; b: { y: number } };
  const active = fromState((s: State) => (s.tab === 'a' ? s.a.x : s.b.y));
  const seen: number[] = [];
  store.subscribe(active, (v) => seen.push(v));

  store.set(['a', 'x'], 10); // active branch
  store.set(['b', 'y'], 20); // inactive branch: no recompute, no notify
  assert.deepEqual(seen, [10]);
  assert.equal(store.nodeStats(active).recomputes, 2);

  store.set(['tab'], 'b'); // switch branch -> now tracks b.y
  assert.deepEqual(seen, [10, 20]);

  store.set(['a', 'x'], 99); // old branch revoked
  assert.deepEqual(seen, [10, 20]);
  assert.equal(store.nodeStats(active).recomputes, 3);

  store.set(['b', 'y'], 30); // new branch tracked
  assert.deepEqual(seen, [10, 20, 30]);
  assert.equal(store.nodeStats(active).recomputes, 4);
});

test('equal projection after recompute does not propagate downstream', () => {
  const store = createStore({ list: [1, 2, 3] });
  const length = fromState((s: { list: number[] }) => s.list.length);
  const doubled = createSelector([length], (n: number) => n * 2);
  const seen: number[] = [];
  store.subscribe(doubled, (v) => seen.push(v));

  store.set(['list'], [9, 8, 7]); // same length
  assert.deepEqual(seen, []);
  assert.equal(store.nodeStats(length).recomputes, 2); // recomputed...
  assert.equal(store.nodeStats(doubled).recomputes, 1); // ...but not propagated
});
