import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { and, eq, gt, or } from '../src/expr.ts';
import { InMemoryAttrStore } from '../src/store.ts';

test('false AND returns only the falsifying branch as the minimal slice', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', and(eq('a', 1), eq('b', 2)));
  store.set('s', 'a', 0); // first conjunct false -> short-circuit
  store.set('s', 'b', 2);
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'false');
  assert.deepEqual(
    d.evidence!.map((e) => e.attr),
    ['a'],
  );
  assert.equal(d.explanation!.children!.length, 1);
});

test('unknown outcome keeps only the unknown branches in the slice', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', and(gt('a', 10), eq('b', 1)));
  store.set('s', 'a', 42); // true; b missing -> unknown
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'unknown');
  assert.deepEqual(
    d.evidence!.map((e) => e.attr),
    ['b'],
  );
  assert.equal(d.evidence![0]!.value, undefined); // missing at decision time
});

test('true OR slice is the single succeeding branch', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', or(eq('a', 1), eq('b', 2)));
  store.set('s', 'a', 1);
  store.set('s', 'b', 9);
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'true');
  assert.deepEqual(
    d.evidence!.map((e) => e.attr),
    ['a'],
  );
  assert.equal(store.readCount('s', 'b'), 0); // short-circuited before reading b
});

test('all-true AND keeps every branch (all are load-bearing)', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', and(eq('a', 1), eq('b', 2)));
  store.set('s', 'a', 1);
  store.set('s', 'b', 2);
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'true');
  assert.deepEqual(
    d.evidence!.map((e) => e.attr),
    ['a', 'b'],
  );
});
