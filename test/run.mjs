import assert from 'node:assert/strict';
import { DynamicTransitiveClosure } from '../dist/dynamic-transitive-closure.js';
import { fullClosure, isValidPath, pairKey, toKeySet, pairListToMap } from './oracle.mjs';

const tests = [];
function test(name, fn) {
  tests.push([name, fn]);
}

function keySetOf(list) {
  return new Set(list.map(([u, v]) => pairKey(u, v)));
}

function assertPairs(actualList, expectedList, what) {
  const actual = keySetOf(actualList);
  const expected = keySetOf(expectedList);
  assert.deepEqual(actual, expected, what + ' (actual: ' + [...actual].join(', ') + ')');
}

// ---------- unit tests ----------

test('chain insertion produces transitive pairs and a witness path', () => {
  const g = new DynamicTransitiveClosure();
  const d = g.applyBatch({ add: [['a', 'b'], ['b', 'c']] });
  assertPairs(d.added, [['a', 'b'], ['b', 'c'], ['a', 'c']], 'delta.added');
  assert.equal(d.removed.length, 0);
  assert.deepEqual(g.witnessPath('a', 'c'), ['a', 'b', 'c']);
  assert.equal(g.isReachable('a', 'a'), false);
  assert.equal(g.witnessPath('c', 'a'), null);
  assert.equal(g.pairCount, 3);
});

test('diamond: deleting one edge keeps reachability via the other path', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['a', 'c'], ['b', 'd'], ['c', 'd']] });
  const d = g.removeEdge('a', 'b');
  assertPairs(d.removed, [['a', 'b']], 'only the direct pair may disappear');
  assert.equal(g.isReachable('a', 'd'), true, 'a still reaches d via c');
  assert.deepEqual(g.witnessPath('a', 'd'), ['a', 'c', 'd']);
  assert.equal(g.isReachable('b', 'd'), true);
});

test('duplicate edges require duplicate removals', () => {
  const g = new DynamicTransitiveClosure();
  g.addEdge('x', 'y');
  g.addEdge('x', 'y');
  assert.equal(g.edgeMultiplicity('x', 'y'), 2);
  const d1 = g.removeEdge('x', 'y');
  assert.equal(d1.removed.length, 0, 'no reachability change while a copy remains');
  assert.equal(g.isReachable('x', 'y'), true);
  const d2 = g.removeEdge('x', 'y');
  assertPairs(d2.removed, [['x', 'y']], 'pair disappears with the last copy');
  assert.equal(g.isReachable('x', 'y'), false);
});

test('duplicate edge through an intermediate node keeps pair alive', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['a', 'b'], ['b', 'c']] });
  const d = g.removeEdge('a', 'b');
  assert.equal(d.removed.length, 0, 'a->b and a->c survive on the second copy');
  assert.equal(g.isReachable('a', 'c'), true);
});

test('self loop is a non-trivial self pair', () => {
  const g = new DynamicTransitiveClosure();
  const d = g.addEdge('a', 'a');
  assertPairs(d.added, [['a', 'a']], 'self pair reported');
  assert.deepEqual(g.witnessPath('a', 'a'), ['a', 'a']);
  const d2 = g.removeEdge('a', 'a');
  assertPairs(d2.removed, [['a', 'a']], 'self pair removed');
  assert.equal(g.isReachable('a', 'a'), false);
});

test('cycle: every node reaches every node including itself', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['b', 'c'], ['c', 'a']] });
  assert.equal(g.pairCount, 9);
  assert.deepEqual(g.witnessPath('a', 'a'), ['a', 'b', 'c', 'a']);
  const d = g.removeEdge('c', 'a');
  assertPairs(
    d.removed,
    [['c', 'a'], ['a', 'a'], ['b', 'a'], ['b', 'b'], ['c', 'b'], ['c', 'c']],
    'breaking the cycle removes exactly the cycle-dependent pairs'
  );
  assertPairs(g.snapshot(), [['a', 'b'], ['a', 'c'], ['b', 'c']], 'remaining pairs');
});

test('cycle-supported pairs collapse only when the entry edge goes', () => {
  // b <-> c form a cycle reachable only through a -> b.
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['b', 'c'], ['c', 'b']] });
  assert.equal(g.isReachable('a', 'c'), true);
  const d = g.removeEdge('a', 'b');
  assertPairs(d.removed, [['a', 'b'], ['a', 'c']], 'a loses the whole cycle');
  assert.equal(g.isReachable('b', 'c'), true);
  assert.equal(g.isReachable('c', 'b'), true);
  assert.equal(g.isReachable('b', 'b'), true, 'b is still on a cycle');
});

test('removing a missing edge throws and leaves the graph untouched', () => {
  const g = new DynamicTransitiveClosure();
  g.addEdge('a', 'b');
  assert.throws(() => g.applyBatch({ remove: [['a', 'b'], ['a', 'b']] }), /not present/);
  assert.throws(() => g.removeEdge('b', 'a'), /not present/);
  assert.equal(g.isReachable('a', 'b'), true);
  assert.equal(g.edgeMultiplicity('a', 'b'), 1);
  assert.equal(g.edgeCount, 1);
});

test('add and remove of the same edge in one batch nets out', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['b', 'c']] });
  const d = g.applyBatch({ add: [['a', 'b']], remove: [['a', 'b']] });
  assert.equal(d.added.length, 0);
  assert.equal(d.removed.length, 0);
  assert.equal(g.isReachable('a', 'c'), true);
  assert.equal(g.edgeMultiplicity('a', 'b'), 1);
});

test('batch delta is exactly the symmetric difference of the relation', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['b', 'c'], ['c', 'd'], ['a', 'e'], ['e', 'd']] });
  const before = g.snapshot();
  const d = g.applyBatch({ remove: [['b', 'c']], add: [['c', 'a']] });
  // a->d survives via a->e->d; a->c and everything behind b->c is gone for a and b.
  assert.equal(g.isReachable('a', 'd'), true);
  assertPairs(d.removed, [['a', 'c'], ['b', 'c'], ['b', 'd']], 'removed pairs');
  // c->a does not close a cycle because b->c is removed in the same batch;
  // c newly reaches a, and through a also b and e (c->d already existed).
  const expectedAdded = [['c', 'a'], ['c', 'b'], ['c', 'e']];
  assertPairs(d.added, expectedAdded, 'added pairs');
  const after = g.snapshot();
  const recompute = keySetOf(before);
  for (const [u, v] of d.removed) recompute.delete(pairKey(u, v));
  for (const [u, v] of d.added) recompute.add(pairKey(u, v));
  assert.deepEqual(keySetOf(after), recompute, 'before - removed + added == after');
});

test('clear resets the structure', () => {
  const g = new DynamicTransitiveClosure();
  g.applyBatch({ add: [['a', 'b'], ['b', 'c']] });
  g.clear();
  assert.equal(g.pairCount, 0);
  assert.equal(g.edgeCount, 0);
  assert.equal(g.nodeCount, 0);
  assert.equal(g.witnessPath('a', 'b'), null);
});

// ---------- randomized differential tests against full recompute ----------

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function checkAgainstOracle(g, multiset, prevPairs, delta, nodeCount, ctx) {
  const oracle = fullClosure(multiset);
  const libPairs = pairListToMap(g.snapshot());

  // 1. The dynamic relation equals the from-scratch relation.
  assert.deepEqual(toKeySet(libPairs), toKeySet(oracle.pairs), ctx + ': relation mismatch');

  // 2. The reported delta transforms the previous relation into the new one.
  const prev = toKeySet(prevPairs);
  const next = toKeySet(oracle.pairs);
  const added = keySetOf(delta.added);
  const removed = keySetOf(delta.removed);
  for (const k of added) {
    assert.ok(!prev.has(k), ctx + ': added pair was already reachable: ' + k);
    assert.ok(next.has(k), ctx + ': added pair not reachable after batch: ' + k);
  }
  for (const k of removed) {
    assert.ok(prev.has(k), ctx + ': removed pair was not reachable before: ' + k);
    assert.ok(!next.has(k), ctx + ': removed pair still reachable after batch: ' + k);
  }
  const reconstructed = new Set(prev);
  for (const k of removed) reconstructed.delete(k);
  for (const k of added) reconstructed.add(k);
  assert.deepEqual(reconstructed, next, ctx + ': delta does not reconstruct the new relation');

  // 3. Every reported pair comes with a valid witness path in the current graph.
  for (const [u, vs] of oracle.pairs) {
    for (const v of vs) {
      const path = g.witnessPath(u, v);
      assert.ok(path !== null, ctx + ': missing witness for ' + pairKey(u, v));
      assert.ok(path.length >= 2, ctx + ': witness too short for ' + pairKey(u, v));
      assert.equal(path[0], u, ctx + ': witness start');
      assert.equal(path[path.length - 1], v, ctx + ': witness end');
      assert.ok(isValidPath(oracle.adj, path), ctx + ': invalid witness ' + JSON.stringify(path));
    }
  }

  // 4. Non-pairs are consistently negative.
  for (let u = 0; u < nodeCount; u++) {
    for (let v = 0; v < nodeCount; v++) {
      const reachable = oracle.pairs.get(u)?.has(v) ?? false;
      assert.equal(g.isReachable(u, v), reachable, ctx + ': isReachable ' + pairKey(u, v));
      if (!reachable) assert.equal(g.witnessPath(u, v), null, ctx + ': witness must be null');
    }
  }

  // 5. Bookkeeping counters.
  let total = 0;
  for (const vs of oracle.pairs.values()) total += vs.size;
  assert.equal(g.pairCount, total, ctx + ': pairCount');
  assert.equal(g.edgeCount, multiset.length, ctx + ': edgeCount');

  return oracle.pairs;
}

test('randomized batches match full recompute (cycles, duplicates, self loops)', () => {
  const SEEDS = 30;
  const ROUNDS = 60;
  const N = 6;
  for (let seed = 1; seed <= SEEDS; seed++) {
    const rng = mulberry32(seed);
    const ri = (n) => Math.floor(rng() * n);
    const g = new DynamicTransitiveClosure();
    const multiset = [];
    let prevPairs = new Map();
    for (let round = 0; round < ROUNDS; round++) {
      const ctx = 'seed=' + seed + ' round=' + round;
      const adds = [];
      const rems = [];
      const nAdd = ri(5);
      for (let k = 0; k < nAdd; k++) adds.push([ri(N), ri(N)]);
      // Pick removals from the current multiset, respecting multiplicities.
      const avail = new Map();
      for (const [a, b] of multiset) {
        const key = a * N + b;
        avail.set(key, (avail.get(key) ?? 0) + 1);
      }
      const keys = [...avail.keys()];
      const nRem = Math.min(ri(4), keys.length);
      for (let k = 0; k < nRem; k++) {
        const key = keys[ri(keys.length)];
        if ((avail.get(key) ?? 0) === 0) continue;
        avail.set(key, avail.get(key) - 1);
        rems.push([Math.floor(key / N), key % N]);
      }
      const delta = g.applyBatch({ add: adds, remove: rems });
      for (const e of adds) multiset.push(e);
      for (const [a, b] of rems) {
        const i = multiset.findIndex(([x, y]) => x === a && y === b);
        assert.ok(i >= 0, ctx + ': multiset bookkeeping');
        multiset.splice(i, 1);
      }
      prevPairs = checkAgainstOracle(g, multiset, prevPairs, delta, N, ctx);
    }
  }
});

test('larger sparse random graphs stay consistent', () => {
  const rng = mulberry32(987654321);
  const ri = (n) => Math.floor(rng() * n);
  const N = 24;
  const g = new DynamicTransitiveClosure();
  const multiset = [];
  let prevPairs = new Map();
  for (let round = 0; round < 40; round++) {
    const ctx = 'large round=' + round;
    const adds = [];
    const rems = [];
    for (let k = 0, n = ri(9); k < n; k++) adds.push([ri(N), ri(N)]);
    const avail = new Map();
    for (const [a, b] of multiset) {
      const key = a * N + b;
      avail.set(key, (avail.get(key) ?? 0) + 1);
    }
    const keys = [...avail.keys()];
    for (let k = 0, n = Math.min(ri(6), keys.length); k < n; k++) {
      const key = keys[ri(keys.length)];
      if ((avail.get(key) ?? 0) === 0) continue;
      avail.set(key, avail.get(key) - 1);
      rems.push([Math.floor(key / N), key % N]);
    }
    const delta = g.applyBatch({ add: adds, remove: rems });
    for (const e of adds) multiset.push(e);
    for (const [a, b] of rems) {
      const i = multiset.findIndex(([x, y]) => x === a && y === b);
      multiset.splice(i, 1);
    }
    prevPairs = checkAgainstOracle(g, multiset, prevPairs, delta, N, ctx);
  }
});

// ---------- runner ----------

let failures = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log('ok   - ' + name);
  } catch (err) {
    failures++;
    console.error('FAIL - ' + name);
    console.error(err instanceof Error ? err.message : err);
  }
}
console.log('');
if (failures > 0) {
  console.error(failures + ' of ' + tests.length + ' tests failed');
  process.exit(1);
}
console.log('all ' + tests.length + ' tests passed');
