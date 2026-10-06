import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { bucketOf } from '../src/hash';
import { FlagConfig } from '../src/types';

const users = Array.from({ length: 100 }, (_, i) => ({ id: `u-${i}` }));

function grayFlags(percentage: number): FlagConfig[] {
  return [
    {
      id: 'gray',
      defaultValue: 'off',
      groups: [
        { id: 'g-rollout', priority: 1, conditions: [], value: 'on', rollout: { percentage } },
      ],
    },
    { id: 'stable', defaultValue: 'A' },
  ];
}

test('hotswap: full evaluation verifies gray rollout 10% -> 50%', () => {
  const store = new FlagStore(grayFlags(10));

  const before = users.map((u) => store.evaluateAll(u));
  const onBefore = new Set(
    users.filter((_, i) => before[i]['gray'].value === 'on').map((u) => u.id),
  );
  // Cross-checked against an independent FNV-1a implementation.
  assert.strictEqual(onBefore.size, 6);
  for (const r of before) assert.strictEqual(r['stable'].value, 'A');

  // Hot-swap the gray config to 50%.
  store.update(grayFlags(50));
  const after = users.map((u) => store.evaluateAll(u));
  const onAfter = new Set(
    users.filter((_, i) => after[i]['gray'].value === 'on').map((u) => u.id),
  );
  assert.strictEqual(onAfter.size, 50);

  // Same salt => same buckets => rollout grows monotonically, nobody flips off.
  for (const id of onBefore) assert.ok(onAfter.has(id), `${id} stays on`);

  // Every flipped user has bucket in [1000, 5000); every steady user is outside.
  for (const u of users) {
    const bucket = bucketOf(`gray:${u.id}`);
    const wasOn = onBefore.has(u.id);
    const isOn = onAfter.has(u.id);
    if (!wasOn && isOn) assert.ok(bucket >= 1000 && bucket < 5000, u.id);
    if (isOn) assert.ok(bucket < 5000, u.id);
    if (!isOn) assert.ok(bucket >= 5000, u.id);
  }

  // The pre-swap snapshot is retained and fully reproducible.
  const replay = users.map((u) => store.evaluateAll(u, { version: 1 }));
  for (let i = 0; i < users.length; i++) {
    assert.strictEqual(replay[i]['gray'].value, before[i]['gray'].value);
    assert.strictEqual(replay[i]['gray'].snapshotVersion, 1);
    assert.strictEqual(after[i]['gray'].snapshotVersion, 2);
  }
});

test('hotswap: cached pre-swap results never leak into the new snapshot', () => {
  const store = new FlagStore(grayFlags(10));
  const user = users.find((u) => {
    const b = bucketOf(`gray:${u.id}`);
    return b >= 1000 && b < 5000;
  })!;
  assert.strictEqual(store.evaluate('gray', user).value, 'off'); // cached at v1
  store.update(grayFlags(50));
  const r = store.evaluate('gray', user); // must re-evaluate at v2
  assert.strictEqual(r.value, 'on');
  assert.strictEqual(r.snapshotVersion, 2);
});
