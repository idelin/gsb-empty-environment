import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { and, eq, gt, inSet } from '../src/expr.ts';
import { AbortError, InMemoryAttrStore } from '../src/store.ts';

test('two subjects have isolated cache entries and decisions', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('role-check', inSet('role', [1, 2]));
  store.set('alice', 'role', 1);
  store.set('bob', 'role', 9);

  const a1 = await engine.evaluate('role-check', 'alice');
  const b1 = await engine.evaluate('role-check', 'bob');
  assert.equal(a1.decision, 'true');
  assert.equal(b1.decision, 'false');

  const a2 = await engine.evaluate('role-check', 'alice');
  const b2 = await engine.evaluate('role-check', 'bob');
  assert.equal(a2.fromCache, true);
  assert.equal(b2.fromCache, true);
  assert.equal(a2.decision, 'true'); // alice's allow never leaks into bob's entry
  assert.equal(b2.decision, 'false');
  assert.equal(store.readCount('alice', 'role'), 1);
  assert.equal(store.readCount('bob', 'role'), 1);
  assert.equal(engine.cacheSize, 2);
});

test('attribute hot-update invalidates only dependent cache entries', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('rule-x', eq('x', 1));
  engine.setRule('rule-y', eq('y', 1));
  store.set('sam', 'x', 1);
  store.set('sam', 'y', 1);

  await engine.evaluate('rule-x', 'sam');
  await engine.evaluate('rule-y', 'sam');
  assert.equal(engine.cacheSize, 2);

  store.set('sam', 'x', 0); // hot update of x only
  assert.equal(engine.cacheSize, 1); // only rule-x's entry was evicted

  const yAgain = await engine.evaluate('rule-y', 'sam');
  assert.equal(yAgain.fromCache, true); // rule-y untouched
  assert.equal(store.readCount('sam', 'y'), 1);

  const xAgain = await engine.evaluate('rule-x', 'sam');
  assert.equal(xAgain.fromCache, false); // rule-x recomputed
  assert.equal(xAgain.decision, 'false');
});

test('missing-attribute reads are tracked so later updates invalidate unknowns', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', and(gt('score', 10), eq('flag', 1)));
  store.set('s', 'flag', 1);
  // 'score' is missing -> unknown
  const d1 = await engine.evaluate('r', 's');
  assert.equal(d1.decision, 'unknown');

  store.set('s', 'score', 42); // attribute appears later
  const d2 = await engine.evaluate('r', 's');
  assert.equal(d2.fromCache, false); // stale unknown was evicted
  assert.equal(d2.decision, 'true');
});

test('rule hot-update invalidates only that rule', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r1', eq('x', 1));
  engine.setRule('r2', eq('x', 1));
  store.set('s', 'x', 1);
  await engine.evaluate('r1', 's');
  await engine.evaluate('r2', 's');
  assert.equal(engine.cacheSize, 2);

  engine.setRule('r1', eq('x', 2)); // hot-update r1 only
  assert.equal(engine.cacheSize, 1);
  const d1 = await engine.evaluate('r1', 's');
  assert.equal(d1.fromCache, false);
  assert.equal(d1.decision, 'false'); // new rule semantics
  const d2 = await engine.evaluate('r2', 's');
  assert.equal(d2.fromCache, true); // r2 cache survived
});
test('a failed attribute read leaves no reusable (allow) cache entry', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', eq('z', 1));
  store.set('s', 'z', 1); // would allow if read successfully

  store.failNextRead((_s, a) => a === 'z', new Error('backend timeout'));
  const d1 = await engine.evaluate('r', 's');
  assert.equal(d1.status, 'error');
  assert.equal(d1.decision, undefined);
  assert.equal(engine.cacheSize, 0); // nothing cached on failure

  const d2 = await engine.evaluate('r', 's');
  assert.equal(d2.status, 'ok');
  assert.equal(d2.fromCache, false); // not served from a poisoned cache
  assert.equal(d2.decision, 'true');
});

test('cancellation leaves no reusable cache entry', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', eq('z', 1));
  store.set('s', 'z', 1);

  const controller = new AbortController();
  controller.abort();
  const d1 = await engine.evaluate('r', 's', controller.signal);
  assert.equal(d1.status, 'error');
  assert.equal(engine.cacheSize, 0);

  // mid-flight cancellation: abort while the first attribute is being read
  const controller2 = new AbortController();
  store.addReadHook(() => {
    controller2.abort();
  });
  const d2 = await engine.evaluate('r', 's', controller2.signal);
  assert.equal(d2.status, 'error');
  assert.equal(engine.cacheSize, 0);

  const d3 = await engine.evaluate('r', 's');
  assert.equal(d3.status, 'ok');
  assert.equal(d3.decision, 'true');
  assert.equal(d3.fromCache, false);
});

test('abort error type is used for cancelled reads', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', eq('z', 1));
  const controller = new AbortController();
  controller.abort();
  const d = await engine.evaluate('r', 's', controller.signal);
  assert.equal(d.status, 'error');
  assert.match(d.error ?? '', /abort/i);
  assert.ok(new AbortError() instanceof Error);
});
