import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { and, eq, inSet, lit, or } from '../src/expr.ts';
import { InMemoryAttrStore } from '../src/store.ts';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

test('false AND <expensive attribute> never reads the expensive attribute', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  let expensiveReads = 0;
  store.addReadHook(async (_s, attr) => {
    if (attr === 'expensive') {
      expensiveReads++;
      await sleep(50); // simulate an expensive fetch
    }
  });
  engine.setRule('r', and(lit('false'), inSet('expensive', [1])));
  const d = await engine.evaluate('r', 's');
  assert.equal(d.status, 'ok');
  assert.equal(d.decision, 'false');
  assert.equal(expensiveReads, 0);
  assert.equal(store.readCount('s', 'expensive'), 0);
});

test('true OR <expensive attribute> never reads the expensive attribute', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  engine.setRule('r', or(lit('true'), inSet('expensive', [1])));
  const d = await engine.evaluate('r', 's');
  assert.equal(d.decision, 'true');
  assert.equal(store.readCount('s', 'expensive'), 0);
});
test('attribute updated mid-request triggers a retry, never a mixed-version allow', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store).bindStore(store);
  store.set('s', 'a', 1); // would satisfy a == 1
  store.set('s', 'b', 2); // satisfies b == 2
  engine.setRule('r', and(eq('a', 1), eq('b', 2)));

  // After 'a' is read once, a hot update flips it to 0. A decision mixing
  // the old 'a' with the new state would wrongly allow; the engine must
  // detect the version change and retry on a consistent snapshot.
  let updated = false;
  store.addReadHook((s, attr) => {
    if (attr === 'a' && !updated) {
      updated = true;
      store.set(s, 'a', 0);
    }
  });

  const d = await engine.evaluate('r', 's');
  assert.equal(d.status, 'ok');
  assert.equal(d.attempts, 2); // first attempt saw the version change, retried
  assert.equal(d.decision, 'false'); // consistent snapshot: a is now 0
});

test('continuously changing versions surface as conflict, never as allow', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store, { maxRetries: 2 }).bindStore(store);
  store.set('s', 'a', 1);
  store.set('s', 'b', 2);
  engine.setRule('r', and(eq('a', 1), eq('b', 2)));

  // Every read of 'a' is followed by another write, so no consistent
  // snapshot ever exists.
  store.addReadHook((s, attr) => {
    if (attr === 'a') store.set(s, 'a', 1);
  });

  const d = await engine.evaluate('r', 's');
  assert.equal(d.status, 'conflict');
  assert.equal(d.decision, undefined);
  assert.equal(d.attempts, 3); // 1 initial + 2 retries
  assert.equal(engine.cacheSize, 0); // conflicts are not cached
});
