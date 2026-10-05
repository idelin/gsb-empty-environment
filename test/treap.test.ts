import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MapStore } from '../src/index.ts';
import { mulberry32 } from './util.ts';

test('set/get/remove roundtrip with sorted iteration', () => {
  const store = new MapStore<number, string>();
  let root = store.empty();
  root = store.set(root, 3, 'c');
  root = store.set(root, 1, 'a');
  root = store.set(root, 2, 'b');
  assert.equal(store.get(root, 2), 'b');
  assert.equal(store.get(root, 9), undefined);
  assert.deepEqual([...store.entries(root)], [[1, 'a'], [2, 'b'], [3, 'c']]);
  root = store.remove(root, 2);
  assert.deepEqual([...store.entries(root)], [[1, 'a'], [3, 'c']]);
  assert.equal(store.size(root), 2);
});

test('persistence: old roots are unaffected by later edits', () => {
  const store = new MapStore<number, number>();
  let v1 = store.empty();
  for (let i = 0; i < 100; i++) v1 = store.set(v1, i, i);
  const v2 = store.set(v1, 50, -1);
  const v3 = store.remove(v2, 10);
  assert.equal(store.get(v1, 50), 50);
  assert.equal(store.get(v1, 10), 10);
  assert.equal(store.get(v2, 50), -1);
  assert.equal(store.get(v3, 10), undefined);
  assert.equal(store.size(v1), 100);
  assert.equal(store.size(v3), 99);
});

test('no-op updates and removals return the identical root', () => {
  const store = new MapStore<number, number>();
  let root = store.empty();
  root = store.set(root, 1, 10);
  root = store.set(root, 2, 20);
  assert.equal(store.set(root, 1, 10), root);
  assert.equal(store.remove(root, 99), root);
});

test('randomized stress against a Map model', () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rand = mulberry32(seed);
    const store = new MapStore<number, number>();
    const model = new Map<number, number>();
    let root = store.empty();
    for (let i = 0; i < 500; i++) {
      const k = Math.floor(rand() * 300);
      if (rand() < 0.6) {
        const v = Math.floor(rand() * 1000);
        root = store.set(root, k, v);
        model.set(k, v);
      } else {
        root = store.remove(root, k);
        model.delete(k);
      }
    }
    const expected = [...model.entries()].sort((a, b) => a[0] - b[0]);
    assert.deepEqual([...store.entries(root)], expected, `seed=${seed}`);
    assert.equal(store.size(root), expected.length, `seed=${seed}`);
  }
});

test('rebuilding the same content yields the same digest but distinct nodes', () => {
  const store = new MapStore<number, number>();
  const build = () => {
    let root = store.empty();
    for (let i = 0; i < 50; i++) root = store.set(root, i, i * 7);
    return root;
  };
  const a = build();
  const b = build();
  assert.notEqual(a, b);
  assert.ok(a !== null && b !== null);
  assert.equal(a.hash, b.hash);
});
