import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DocumentStore } from '../src/store.ts';
import { PathError } from '../src/errors.ts';

function makeStore(): DocumentStore {
  return new DocumentStore({
    user: { name: 'ada', age: 36 },
    cart: { items: [{ sku: 'a', qty: 1 }, { sku: 'b', qty: 2 }] },
    meta: { tags: ['x', 'y'] },
  });
}

test('results are cached and bound to the structure version', () => {
  const store = makeStore();
  const first = store.query('user.name');
  const second = store.query('user.name');
  assert.equal(first.fromCache, false);
  assert.equal(second.fromCache, true);
  assert.equal(first.version, 0);
  assert.equal(second.version, 0);
  assert.deepEqual(second.matches.map((m) => m.value), ['ada']);
});

test('local change revises only overlapping results and path evidence', () => {
  const store = makeStore();
  store.query('user.name');
  store.query('cart.items[0].sku');
  assert.equal(store.cacheSize, 2);

  // Unrelated local change: user.name cache entry survives.
  store.set(['cart', 'items', 0, 'qty'], 5);
  assert.equal(store.version, 1);
  assert.equal(store.cacheSize, 1, 'cart query invalidated, user query kept');

  const user = store.query('user.name');
  assert.equal(user.fromCache, true);
  assert.equal(user.version, 1);

  const sku = store.query('cart.items[0].sku');
  assert.equal(sku.fromCache, false, 'cart query was revised');
});

test('mutation updates matched results and their path evidence', () => {
  const store = makeStore();
  const before = store.query('cart.items[*].qty');
  assert.deepEqual(before.matches.map((m) => m.value), [1, 2]);
  assert.deepEqual(before.matches.map((m) => m.path), [
    ['cart', 'items', 0, 'qty'],
    ['cart', 'items', 1, 'qty'],
  ]);

  store.set(['cart', 'items', 1, 'qty'], 9);
  const after = store.query('cart.items[*].qty');
  assert.equal(after.fromCache, false);
  assert.deepEqual(after.matches.map((m) => m.value), [1, 9]);

  store.remove(['cart', 'items', 0]);
  const shrunk = store.query('cart.items[*].qty');
  assert.deepEqual(shrunk.matches.map((m) => m.value), [9]);
  assert.deepEqual(shrunk.matches.map((m) => m.path), [['cart', 'items', 0, 'qty']]);
});

test('store rejects non-data writes and forbidden keys', () => {
  const store = makeStore();
  assert.throws(() => store.set(['user', 'when'], new Date(0)), PathError);
  assert.throws(() => store.set(['user', 'run'], () => 1), PathError);
  assert.throws(
    () => store.set(['user', '__proto__'], {}),
    (e: unknown) => e instanceof PathError && e.code === 'FORBIDDEN_PROPERTY',
  );
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;
  assert.throws(() => store.set(['user', 'cycle'], cyclic), PathError);
  assert.throws(() => new DocumentStore(new WeakMap() as unknown), PathError);
});

test('wildcard query evidence invalidates on child removal', () => {
  const store = makeStore();
  store.query('meta.tags[*]');
  store.remove(['meta', 'tags', 0]);
  const r = store.query('meta.tags[*]');
  assert.equal(r.fromCache, false);
  assert.deepEqual(r.matches.map((m) => m.value), ['y']);
});
