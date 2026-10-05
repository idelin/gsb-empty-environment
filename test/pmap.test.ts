import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PMap } from '../src/index.ts';

test('set/get/has/delete basics', () => {
  const m0 = PMap.empty<number, string>();
  const m1 = m0.set(1, 'a').set(2, 'b').set(3, 'c');
  assert.equal(m1.size, 3);
  assert.equal(m1.get(2), 'b');
  assert.equal(m1.has(4), false);
  const m2 = m1.delete(2);
  assert.equal(m2.has(2), false);
  assert.equal(m2.size, 2);
  assert.equal(m2.get(3), 'c');
});

test('persistence: old versions are untouched', () => {
  const m1 = PMap.empty<number, number>().set(1, 10).set(2, 20);
  const m2 = m1.set(3, 30).delete(1);
  assert.equal(m1.get(1), 10);
  assert.equal(m1.has(3), false);
  assert.equal(m1.size, 2);
  assert.equal(m2.has(1), false);
  assert.equal(m2.get(3), 30);
});

test('no-op updates return the identical map', () => {
  const m1 = PMap.empty<number, number>().set(1, 10);
  assert.equal(m1.set(1, 10), m1);
  assert.equal(m1.delete(99), m1);
});

test('entries are in ascending key order', () => {
  let m = PMap.empty<number, number>();
  const keys = [50, 20, 80, 10, 30, 60, 90, 25, 5];
  for (const k of keys) m = m.set(k, k * 2);
  assert.deepEqual(
    [...m.entries()].map(([k]) => k),
    [...keys].sort((a, b) => a - b),
  );
});

test('stays balanced under sequential inserts', () => {
  let m = PMap.empty<number, number>();
  const n = 10000;
  for (let i = 0; i < n; i++) m = m.set(i, i);
  assert.equal(m.size, n);
  // AVL height bound ~1.44*log2(n); leave generous headroom.
  assert.ok(m.root!.height <= 24, `height ${m.root!.height}`);
  for (let i = 0; i < n; i += 999) assert.equal(m.get(i), i);
});

test('random operations match a Map model', () => {
  let seed = 42;
  const rng = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  let m = PMap.empty<number, number>();
  const model = new Map<number, number>();
  for (let step = 0; step < 5000; step++) {
    const key = Math.floor(rng() * 500);
    if (rng() < 0.6) {
      const value = Math.floor(rng() * 1e6);
      m = m.set(key, value);
      model.set(key, value);
    } else {
      m = m.delete(key);
      model.delete(key);
    }
  }
  assert.equal(m.size, model.size);
  assert.deepEqual(
    [...m.entries()].map(([k, v]) => [k, v]),
    [...model.entries()].sort((a, b) => a[0] - b[0]),
  );
});
