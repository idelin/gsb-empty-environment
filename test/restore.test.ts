import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DefinitionRegistry,
  Store,
  restore,
  save,
  seal,
  type Envelope,
} from '../src/index.ts';

function makeRegistry(): DefinitionRegistry {
  const registry = new DefinitionRegistry();
  registry.register({ id: 'sum', version: 1, compute: ([a, b]) => (a as number) + (b as number) });
  registry.register({ id: 'sum', version: 2, compute: ([a, b]) => (a as number) + (b as number) });
  registry.register({ id: 'double', version: 1, compute: ([x]) => (x as number) * 2 });
  registry.register({
    id: 'max2',
    version: 1,
    compute: ([a, b]) => Math.max(a as number, (b as number) ?? Number.NEGATIVE_INFINITY),
  });
  return registry;
}

function makeStore(): { store: Store; registry: DefinitionRegistry } {
  const registry = makeRegistry();
  const store = new Store(registry);
  store.defineCell('a', 1);
  store.defineCell('b', 2);
  store.defineComputed('total', { id: 'sum', version: 1 }, ['a', 'b']);
  store.defineComputed('grand', { id: 'double', version: 1 }, ['total']);
  store.defineComputed('echo', { id: 'sum', version: 1 }, ['a', 'b']); // shares a, b
  store.beginTx().set('a', 10).set('b', 20).commit();
  return { store, registry };
}

test('round trip: save then restore rebuilds graph and recomputed state', () => {
  const { store, registry } = makeStore();
  const envelope = save(store);
  const restored = new Store(registry);
  const result = restore(restored, JSON.parse(JSON.stringify(envelope)), registry);
  assert.equal(result.ok, true);
  assert.equal(restored.read('a'), 10);
  assert.equal(restored.read('total'), 30); // recomputed from cells, not stored
  assert.equal(restored.read('grand'), 60);
  assert.equal(restored.read('echo'), 30);
});

test('shared entities remain shared after restore', () => {
  const { store, registry } = makeStore();
  const restored = new Store(registry);
  restore(restored, save(store), registry);
  const shared = restored.node('a');
  assert.equal(restored.node('a'), shared); // single interned instance per id
  const total = restored.node('total');
  const echo = restored.node('echo');
  assert.equal(total.kind, 'computed');
  assert.equal(echo.kind, 'computed');
  if (total.kind === 'computed' && echo.kind === 'computed') {
    // both computed nodes reference the same shared cell instance
    assert.equal(restored.node(total.deps[0]), restored.node(echo.deps[0]));
    assert.equal(restored.node(total.deps[0]), shared);
  }
});

test('functions are never serialized into the envelope', () => {
  const { store } = makeStore();
  const json = JSON.stringify(save(store));
  assert.ok(!json.includes('=>'));
  assert.ok(!json.includes('function'));
  for (const node of save(store).nodes) {
    assert.ok(!('compute' in node));
  }
});

test('unknown computation definition blocks atomic publish', () => {
  const { store, registry } = makeStore();
  const envelope = save(store);
  const nodes = envelope.nodes.map((n) =>
    n.kind === 'computed' && n.id === 'total' ? { ...n, definitionId: 'evil' } : n,
  );
  const forged = seal(1, nodes);
  const target = new Store(registry);
  target.defineCell('keep', 'alive');
  const result = restore(target, forged, registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'UNKNOWN_DEFINITION');
  assert.equal(target.read('keep'), 'alive'); // old live state untouched
  assert.equal(target.hasNode('total'), false);
});

test('definition version mismatch blocks atomic publish', () => {
  const { store, registry } = makeStore();
  const nodes = save(store).nodes.map((n) =>
    n.kind === 'computed' && n.id === 'total' ? { ...n, definitionVersion: 99 } : n,
  );
  const result = restore(new Store(registry), seal(1, nodes), registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'DEFINITION_VERSION');
});

test('incompatible schema version is rejected', () => {
  const { store, registry } = makeStore();
  const envelope = save(store);
  const result = restore(new Store(registry), seal(2, envelope.nodes), registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'SCHEMA_VERSION');
});

test('dependency edge to a missing node is rejected', () => {
  const { store, registry } = makeStore();
  const nodes = save(store).nodes.map((n) =>
    n.kind === 'computed' && n.id === 'grand' ? { ...n, deps: ['ghost'] } : n,
  );
  const result = restore(new Store(registry), seal(1, nodes), registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'MISSING_NODE');
});

test('illegal evaluation cycle in saved graph is rejected', () => {
  const registry = makeRegistry();
  const nodes = [
    { kind: 'cell', id: 'x', value: 1 },
    { kind: 'computed', id: 'p', definitionId: 'double', definitionVersion: 1, deps: ['q'], monotonic: false },
    { kind: 'computed', id: 'q', definitionId: 'double', definitionVersion: 1, deps: ['p'], monotonic: false },
  ];
  const result = restore(new Store(registry), seal(1, nodes), registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'EVALUATION_CYCLE');
});

test('allowed monotonic ring restores and converges', () => {
  const registry = makeRegistry();
  const nodes = [
    { kind: 'cell', id: 'x', value: 3 },
    { kind: 'cell', id: 'y', value: 5 },
    { kind: 'computed', id: 'ra', definitionId: 'max2', definitionVersion: 1, deps: ['x', 'rb'], monotonic: true },
    { kind: 'computed', id: 'rb', definitionId: 'max2', definitionVersion: 1, deps: ['y', 'ra'], monotonic: true },
  ];
  const target = new Store(registry);
  const result = restore(target, seal(1, nodes), registry);
  assert.equal(result.ok, true);
  assert.equal(target.read('ra'), 5);
  assert.equal(target.read('rb'), 5);
});

test('subscriber callbacks never run during restore', () => {
  const { store, registry } = makeStore();
  const envelope = save(store);
  let calls = 0;
  store.subscribe('grand', () => {
    calls += 1;
  });
  const changed = JSON.parse(JSON.stringify(envelope)) as Envelope;
  changed.nodes = changed.nodes.map((n) =>
    n.kind === 'cell' && n.id === 'a' ? { ...n, value: 999 } : n,
  );
  const resealed = seal(1, changed.nodes);
  const result = restore(store, resealed, registry);
  assert.equal(result.ok, true);
  assert.equal(calls, 0); // silent during restore
  assert.equal(store.read('grand'), 2 * (999 + 20));
  store.beginTx().set('b', 1).commit();
  assert.equal(calls, 1); // normal commits still notify after restore
});

test('old live state stays readable after failed restore', () => {
  const { store, registry } = makeStore();
  const before = store.snapshot();
  const bad = seal(1, [
    { kind: 'cell', id: 'only', value: 1 },
    { kind: 'computed', id: 'broken', definitionId: 'nope', definitionVersion: 1, deps: ['only'], monotonic: false },
  ]);
  const result = restore(store, bad, registry);
  assert.equal(result.ok, false);
  assert.equal(store.read('grand'), 60);
  assert.equal(store.read('a', before), 10);
  store.beginTx().set('a', 11).commit(); // store still fully functional
  assert.equal(store.read('total'), 31);
});

test('duplicate node ids are rejected', () => {
  const { registry } = makeStore();
  const nodes = [
    { kind: 'cell', id: 'x', value: 1 },
    { kind: 'cell', id: 'x', value: 2 },
  ];
  const result = restore(new Store(registry), seal(1, nodes), registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, 'DUPLICATE_NODE');
});
