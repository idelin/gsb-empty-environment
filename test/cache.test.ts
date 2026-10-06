import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { cacheKey } from '../src/cache';
import { FlagConfig } from '../src/types';

const flagsV1: FlagConfig[] = [
  { id: 'base', defaultValue: 'off' },
  {
    id: 'child',
    defaultValue: 'off',
    fallbackValue: 'blocked',
    dependencies: [{ flagId: 'base', requiredValue: 'on' }],
  },
];

test('cache: key contains attribute version and dependency closure digest', () => {
  const store = new FlagStore(flagsV1);
  const digest = store.getSnapshot().closureDigest('child');
  const key = cacheKey({
    snapshotVersion: 1,
    flagId: 'child',
    userId: 'u-1',
    attrVersion: 3,
    closureDigest: digest,
  });
  assert.ok(key.includes('v1'));
  assert.ok(key.includes('child'));
  assert.ok(key.includes('u-1'));
  assert.ok(key.includes('attr:3'));
  assert.ok(key.includes(`deps:${digest}`));
  // Closure digest reflects the dependency set (1 flag in the closure).
  assert.ok(digest.startsWith('1:'));
});

test('cache: repeated evaluation hits cache; attrVersion bump misses', () => {
  const store = new FlagStore(flagsV1);
  const user = { id: 'u-1', attrVersion: 0 };
  store.evaluate('child', user);
  assert.strictEqual(store.cacheSize, 1);
  store.evaluate('child', user); // hit, no new entry
  assert.strictEqual(store.cacheSize, 1);
  store.evaluate('child', { id: 'u-1', attrVersion: 1 }); // miss: new key
  assert.strictEqual(store.cacheSize, 2);
});

test('cache: attribute change with same attrVersion would serve stale, bumping fixes it', () => {
  const store = new FlagStore([
    {
      id: 'f',
      defaultValue: 'off',
      groups: [
        { id: 'g', priority: 1, conditions: [{ key: 'tier', op: 'eq', value: 'gold' }], value: 'on' },
      ],
    },
  ]);
  const before = store.evaluate('f', { id: 'u-1', attributes: { tier: 'free' }, attrVersion: 0 });
  assert.strictEqual(before.value, 'off');
  const after = store.evaluate('f', { id: 'u-1', attributes: { tier: 'gold' }, attrVersion: 1 });
  assert.strictEqual(after.value, 'on');
});

test('cache: dependency closure digest changes when a dependency config changes', () => {
  const store = new FlagStore(flagsV1);
  const digestBefore = store.getSnapshot().closureDigest('child');
  // Flip base on in a new snapshot.
  store.update([
    { id: 'base', defaultValue: 'on' },
    flagsV1[1],
  ]);
  const digestAfter = store.getSnapshot().closureDigest('child');
  assert.notStrictEqual(digestBefore, digestAfter);
  // New evaluation after hot-swap must not serve the stale cached result.
  const r = store.evaluate('child', { id: 'u-1' });
  assert.strictEqual(r.value, 'off'); // dep met now, so default 'off' not 'blocked'
  assert.strictEqual(r.reason, 'default');
  assert.strictEqual(r.snapshotVersion, 2);
});

test('cache: unrelated flag change does not alter the closure digest', () => {
  const store = new FlagStore([...flagsV1, { id: 'unrelated', defaultValue: 0 }]);
  const d1 = store.getSnapshot().closureDigest('child');
  store.update([...flagsV1, { id: 'unrelated', defaultValue: 999 }]);
  const d2 = store.getSnapshot().closureDigest('child');
  assert.strictEqual(d1, d2);
});

test('cache: can be bypassed per call', () => {
  const store = new FlagStore(flagsV1);
  store.evaluate('child', { id: 'u-1' }, { useCache: false });
  assert.strictEqual(store.cacheSize, 0);
});
