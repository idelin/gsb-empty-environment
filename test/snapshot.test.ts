import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { FlagConfig } from '../src/types';

const v1Flags: FlagConfig[] = [
  {
    id: 'flag-x',
    defaultValue: 'off',
    groups: [
      { id: 'g', priority: 1, conditions: [], value: 'on', rollout: { percentage: 50 } },
    ],
  },
];

test('snapshot: update creates a new version and retains the old one', () => {
  const store = new FlagStore(v1Flags);
  assert.strictEqual(store.version, 1);
  const v2Flags: FlagConfig[] = [
    {
      id: 'flag-x',
      defaultValue: 'off',
      groups: [
        { id: 'g', priority: 1, conditions: [], value: 'on', rollout: { percentage: 100 } },
      ],
    },
  ];
  const v2 = store.update(v2Flags);
  assert.strictEqual(v2, 2);
  assert.deepStrictEqual(store.versions(), [1, 2]);
});

test('snapshot: bucketing parameter changes only affect the new snapshot', () => {
  const store = new FlagStore(v1Flags);
  // u-7469 has bucket 5000 for flag-x: outside the 50% rollout.
  const user = { id: 'u-7469' };
  assert.strictEqual(store.evaluate('flag-x', user).value, 'off');

  // Hot-swap: 50% -> 100% and a new salt.
  store.update([
    {
      id: 'flag-x',
      defaultValue: 'off',
      groups: [
        { id: 'g', priority: 1, conditions: [], value: 'on', rollout: { percentage: 100, salt: 'saltA' } },
      ],
    },
  ]);

  // New snapshot uses the new parameters.
  const now = store.evaluate('flag-x', user);
  assert.strictEqual(now.value, 'on');
  assert.strictEqual(now.snapshotVersion, 2);
  assert.strictEqual(now.bucketKey, 'saltA:flag-x:u-7469');

  // The retained old snapshot still evaluates with the old parameters.
  const then = store.evaluate('flag-x', user, { version: 1 });
  assert.strictEqual(then.value, 'off');
  assert.strictEqual(then.snapshotVersion, 1);
  assert.strictEqual(then.bucket, 5000);
  assert.strictEqual(then.bucketKey, 'flag-x:u-7469');
});

test('snapshot: caller mutation of config after update does not leak in', () => {
  const flags: FlagConfig[] = [{ id: 'f', defaultValue: 'a' }];
  const store = new FlagStore(flags);
  flags[0].defaultValue = 'MUTATED';
  assert.strictEqual(store.evaluate('f', { id: 'u' }).value, 'a');
});

test('snapshot: unknown version throws', () => {
  const store = new FlagStore(v1Flags);
  assert.throws(() => store.getSnapshot(99), /no snapshot at version 99/);
});
