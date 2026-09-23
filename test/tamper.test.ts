import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DefinitionRegistry,
  Store,
  restore,
  save,
  type Envelope,
} from '../src/index.ts';

function setup(): { store: Store; registry: DefinitionRegistry; envelope: Envelope } {
  const registry = new DefinitionRegistry();
  registry.register({ id: 'sum', version: 1, compute: ([a, b]) => (a as number) + (b as number) });
  const store = new Store(registry);
  store.defineCell('a', 1);
  store.defineCell('b', 2);
  store.defineComputed('total', { id: 'sum', version: 1 }, ['a', 'b']);
  store.beginTx().set('a', 10).commit();
  return { store, registry, envelope: save(store) };
}

function clone(envelope: Envelope): Envelope {
  return JSON.parse(JSON.stringify(envelope)) as Envelope;
}

function assertTamperRejected(
  mutate: (envelope: Envelope) => void,
  expectedCode: string,
): void {
  const { registry, envelope } = setup();
  const target = new Store(registry);
  target.defineCell('live', 'readable');
  const tampered = clone(envelope);
  mutate(tampered);
  const result = restore(target, tampered, registry);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.code, expectedCode);
  assert.equal(target.read('live'), 'readable'); // old live state intact
  assert.equal(target.hasNode('total'), false);
}

test('tampered cell value is detected', () => {
  assertTamperRejected((env) => {
    for (const n of env.nodes) if (n.kind === 'cell' && n.id === 'a') n.value = 9999;
  }, 'INTEGRITY');
});

test('tampered definition version is detected', () => {
  assertTamperRejected((env) => {
    for (const n of env.nodes) if (n.kind === 'computed') n.definitionVersion = 2;
  }, 'INTEGRITY');
});

test('tampered dependency edge is detected', () => {
  assertTamperRejected((env) => {
    for (const n of env.nodes) if (n.kind === 'computed') n.deps = ['b'];
  }, 'INTEGRITY');
});

test('forged digest is detected', () => {
  assertTamperRejected((env) => {
    env.digest = 'sha256:' + '0'.repeat(64);
  }, 'INTEGRITY');
});

test('removed node is detected', () => {
  assertTamperRejected((env) => {
    env.nodes = env.nodes.filter((n) => n.id !== 'b');
  }, 'INTEGRITY');
});

test('injected node is detected', () => {
  assertTamperRejected((env) => {
    env.nodes.push({ kind: 'cell', id: 'extra', value: 0 });
  }, 'INTEGRITY');
});

test('flipped monotonic flag is detected', () => {
  assertTamperRejected((env) => {
    for (const n of env.nodes) if (n.kind === 'computed') n.monotonic = true;
  }, 'INTEGRITY');
});

test('malformed envelopes are rejected without touching live state', () => {
  const { registry } = setup();
  const target = new Store(registry);
  target.defineCell('live', 42);
  for (const bad of [null, 42, 'x', {}, { schemaVersion: 1 }, { schemaVersion: 1, nodes: [] }]) {
    const result = restore(target, bad, registry);
    assert.equal(result.ok, false);
    if (!result.ok) assert.equal(result.code, 'INVALID_ENVELOPE');
    assert.equal(target.read('live'), 42);
  }
});
