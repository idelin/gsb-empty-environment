import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DefinitionRegistry,
  GraphError,
  Store,
  replay,
  save,
} from '../src/index.ts';

function makeRegistry(): DefinitionRegistry {
  const registry = new DefinitionRegistry();
  registry.register({ id: 'sum', version: 1, compute: ([a, b]) => (a as number) + (b as number) });
  registry.register({ id: 'double', version: 1, compute: ([x]) => (x as number) * 2 });
  registry.register({
    id: 'max2',
    version: 1,
    compute: ([a, b]) => Math.max(a as number, (b as number) ?? Number.NEGATIVE_INFINITY),
  });
  return registry;
}

function makeStore(): Store {
  const store = new Store(makeRegistry());
  store.defineCell('a', 1);
  store.defineCell('b', 2);
  store.defineComputed('total', { id: 'sum', version: 1 }, ['a', 'b']);
  store.defineComputed('grand', { id: 'double', version: 1 }, ['total']);
  return store;
}

test('transaction commits atomically with a single notification per node', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  store.subscribe('grand', (value) => seen.push(value));
  store.beginTx().set('a', 10).set('b', 20).commit();
  assert.equal(store.read('grand'), 60);
  assert.deepEqual(seen, [60]); // not [20, 60]: no intermediate states observed
});

test('readers never see transaction intermediate state', () => {
  const store = makeStore();
  const before = store.snapshot();
  const tx = store.beginTx();
  tx.set('a', 100);
  assert.equal(store.read('a'), 1); // uncommitted op invisible
  tx.set('b', 200);
  tx.commit();
  assert.equal(store.read('a'), 100);
  assert.equal(store.read('a', before), 1); // old snapshot still reads old state
  assert.equal(store.read('grand', before), 6);
});

test('failed commit leaves state and log untouched', () => {
  const store = makeStore();
  const logSize = store.log.length;
  assert.throws(
    () => store.beginTx().set('a', 5).set('nope', 1).commit(),
    (err: unknown) => err instanceof GraphError && err.code === 'UNKNOWN_NODE',
  );
  assert.equal(store.read('a'), 1);
  assert.equal(store.log.length, logSize);
  assert.throws(
    () => store.beginTx().set('total', 5).commit(),
    (err: unknown) => err instanceof GraphError && err.code === 'NOT_A_CELL',
  );
  assert.equal(store.read('total'), 3);
});

test('illegal evaluation cycle rejected at define time', () => {
  const store = new Store(makeRegistry());
  store.defineCell('x', 1);
  assert.throws(
    () =>
      store.defineGraph([
        { kind: 'computed', id: 'p', definition: { id: 'double', version: 1 }, deps: ['q'] },
        { kind: 'computed', id: 'q', definition: { id: 'double', version: 1 }, deps: ['p'] },
      ]),
    (err: unknown) => err instanceof GraphError && err.code === 'EVALUATION_CYCLE',
  );
  assert.equal(store.hasNode('p'), false); // rejected atomically
  assert.equal(store.hasNode('q'), false);
});

test('self-loop on non-monotonic computed is an illegal evaluation cycle', () => {
  const store = new Store(makeRegistry());
  store.defineCell('x', 1);
  assert.throws(
    () =>
      store.defineGraph([
        { kind: 'computed', id: 's', definition: { id: 'double', version: 1 }, deps: ['x', 's'] },
      ]),
    (err: unknown) => err instanceof GraphError && err.code === 'EVALUATION_CYCLE',
  );
});

test('monotonic ring is allowed and converges to fixpoint', () => {
  const store = new Store(makeRegistry());
  store.defineGraph([
    { kind: 'cell', id: 'x', initial: 3 },
    { kind: 'cell', id: 'y', initial: 5 },
    { kind: 'computed', id: 'ra', definition: { id: 'max2', version: 1 }, deps: ['x', 'rb'], monotonic: true },
    { kind: 'computed', id: 'rb', definition: { id: 'max2', version: 1 }, deps: ['y', 'ra'], monotonic: true },
  ]);
  assert.equal(store.read('ra'), 5);
  assert.equal(store.read('rb'), 5);
  store.beginTx().set('x', 10).commit();
  assert.equal(store.read('ra'), 10);
  assert.equal(store.read('rb'), 10);
});

test('non-converging monotonic ring is reported separately', () => {
  const registry = makeRegistry();
  registry.register({
    id: 'succ',
    version: 1,
    compute: ([, self]) => ((self as number) ?? 0) + 1,
  });
  const store = new Store(registry);
  store.defineCell('x', 1);
  assert.throws(
    () =>
      store.defineGraph([
        { kind: 'computed', id: 'loop', definition: { id: 'succ', version: 1 }, deps: ['x', 'loop'], monotonic: true },
      ]),
    (err: unknown) => err instanceof GraphError && err.code === 'MONOTONIC_NONCONVERGENCE',
  );
  assert.equal(store.hasNode('loop'), false);
});

test('operation log replays to identical state', () => {
  const store = makeStore();
  store.beginTx().set('a', 7).commit();
  store.beginTx().set('b', 8).set('a', 9).commit();
  const clone = replay(store.log, makeRegistry());
  assert.deepEqual(save(clone), save(store));
});

test('tx cannot be reused after commit or rollback', () => {
  const store = makeStore();
  const tx = store.beginTx();
  tx.set('a', 5);
  tx.commit();
  assert.throws(() => tx.set('a', 6), /closed/);
  const tx2 = store.beginTx();
  tx2.rollback();
  assert.throws(() => tx2.commit(), /closed/);
  assert.equal(store.read('a'), 5);
});
