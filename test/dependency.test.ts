import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { DependencyCycleError, ConfigValidationError } from '../src/snapshot';
import { FlagConfig } from '../src/types';

const base: FlagConfig = { id: 'base', defaultValue: 'off' };
const child: FlagConfig = {
  id: 'child',
  defaultValue: 'off',
  fallbackValue: 'blocked',
  dependencies: [{ flagId: 'base', requiredValue: 'on' }],
};

test('dependency: unmet dependency yields fallback with explanation', () => {
  const store = new FlagStore([base, child]);
  const r = store.evaluate('child', { id: 'u-1' });
  assert.strictEqual(r.value, 'blocked');
  assert.strictEqual(r.reason, 'dependency_unmet');
  assert.deepStrictEqual(r.dependencies, [
    { flagId: 'base', requiredValue: 'on', actualValue: 'off', met: false },
  ]);
});

test('dependency: met dependency lets normal evaluation proceed', () => {
  const store = new FlagStore([
    { id: 'base', defaultValue: 'on' },
    child,
  ]);
  const r = store.evaluate('child', { id: 'u-1' });
  assert.strictEqual(r.value, 'off');
  assert.strictEqual(r.reason, 'default');
  assert.strictEqual(r.dependencies[0].met, true);
});

test('dependency: transitive chain evaluates on one snapshot', () => {
  const store = new FlagStore([
    { id: 'a', defaultValue: 'on' },
    { id: 'b', defaultValue: 'x', dependencies: [{ flagId: 'a', requiredValue: 'on' }] },
    { id: 'c', defaultValue: 'y', dependencies: [{ flagId: 'b', requiredValue: 'x' }] },
  ]);
  const r = store.evaluate('c', { id: 'u-1' });
  assert.strictEqual(r.value, 'y');
  assert.strictEqual(r.reason, 'default');
});

test('dependency: all conditions evaluate on the same snapshot', () => {
  const store = new FlagStore([
    { id: 'base', defaultValue: 'off' },
    { ...child },
  ]);
  // Atomic swap: base flips on AND child loses its fallback, in one update.
  store.update([
    { id: 'base', defaultValue: 'on' },
    { id: 'child', defaultValue: 'live', dependencies: [{ flagId: 'base', requiredValue: 'on' }] },
  ]);
  const r = store.evaluate('child', { id: 'u-1' });
  assert.strictEqual(r.value, 'live');
  assert.strictEqual(r.snapshotVersion, 2);
  // Old snapshot still shows the old combined state.
  const old = store.evaluate('child', { id: 'u-1' }, { version: 1 });
  assert.strictEqual(old.value, 'blocked');
  assert.strictEqual(old.snapshotVersion, 1);
});

test('dependency: cycle rejects the entire update atomically', () => {
  const store = new FlagStore([{ id: 'a', defaultValue: 1 }]);
  const cyclic: FlagConfig[] = [
    { id: 'a', defaultValue: 1, dependencies: [{ flagId: 'b', requiredValue: 1 }] },
    { id: 'b', defaultValue: 1, dependencies: [{ flagId: 'c', requiredValue: 1 }] },
    { id: 'c', defaultValue: 1, dependencies: [{ flagId: 'a', requiredValue: 1 }] },
  ];
  assert.throws(() => store.update(cyclic), DependencyCycleError);
  // Store is untouched: version and evaluation unchanged.
  assert.strictEqual(store.version, 1);
  assert.strictEqual(store.evaluate('a', { id: 'u' }).value, 1);
  assert.deepStrictEqual(store.versions(), [1]);
});

test('dependency: self-dependency is a cycle', () => {
  const store = new FlagStore();
  assert.throws(
    () => store.update([{ id: 'a', defaultValue: 1, dependencies: [{ flagId: 'a', requiredValue: 1 }] }]),
    DependencyCycleError,
  );
  assert.strictEqual(store.version, 0);
});

test('dependency: dangling reference rejects the update', () => {
  const store = new FlagStore();
  assert.throws(
    () => store.update([{ id: 'a', defaultValue: 1, dependencies: [{ flagId: 'ghost', requiredValue: 1 }] }]),
    ConfigValidationError,
  );
  assert.strictEqual(store.version, 0);
});

test('dependency: duplicate flag ids reject the update', () => {
  const store = new FlagStore();
  assert.throws(
    () => store.update([{ id: 'a', defaultValue: 1 }, { id: 'a', defaultValue: 2 }]),
    ConfigValidationError,
  );
});
