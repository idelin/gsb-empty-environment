import { test, assert } from './harness';
import { FlagStore } from '../src/store';
import { bucketOf } from '../src/hash';
import { FlagConfig } from '../src/types';

// Bucket fixtures cross-checked against an independent FNV-1a implementation:
//   bucket('flag-x:u-8118')  = 0      bucket('flag-x:u-15531') = 999
//   bucket('flag-x:u-17590') = 1000   bucket('flag-x:u-8656')  = 4999
//   bucket('flag-x:u-7469')  = 5000   bucket('flag-x:u-21846') = 9999
//   bucket('saltA:flag-x:u-3185') = 5000
const FIXTURES: Array<[string, number]> = [
  ['u-8118', 0],
  ['u-15531', 999],
  ['u-17590', 1000],
  ['u-8656', 4999],
  ['u-7469', 5000],
  ['u-21846', 9999],
];

function flagWithRollout(percentage: number, salt?: string): FlagConfig {
  return {
    id: 'flag-x',
    defaultValue: 'off',
    groups: [
      { id: 'g', priority: 1, conditions: [], value: 'on', rollout: { percentage, salt } },
    ],
  };
}

test('boundary: fixture buckets match the library hash', () => {
  for (const [user, expected] of FIXTURES) {
    assert.strictEqual(bucketOf(`flag-x:${user}`), expected, user);
  }
  assert.strictEqual(bucketOf('saltA:flag-x:u-3185'), 5000);
});

test('boundary: bucket == threshold*100 fails, threshold-1 passes (10%)', () => {
  const store = new FlagStore([flagWithRollout(10)]);
  // bucket 999 < 1000 -> in
  const inside = store.evaluate('flag-x', { id: 'u-15531' });
  assert.strictEqual(inside.value, 'on');
  assert.strictEqual(inside.bucket, 999);
  // bucket 1000 is NOT < 1000 -> out
  const outside = store.evaluate('flag-x', { id: 'u-17590' });
  assert.strictEqual(outside.value, 'off');
  assert.strictEqual(outside.reason, 'default');
});

test('boundary: 50% rollout edges', () => {
  const store = new FlagStore([flagWithRollout(50)]);
  assert.strictEqual(store.evaluate('flag-x', { id: 'u-8656' }).value, 'on'); // 4999
  assert.strictEqual(store.evaluate('flag-x', { id: 'u-7469' }).value, 'off'); // 5000
});

test('boundary: 0% lets nobody in, 100% lets everybody in', () => {
  const zero = new FlagStore([flagWithRollout(0)]);
  assert.strictEqual(zero.evaluate('flag-x', { id: 'u-8118' }).value, 'off'); // bucket 0
  const full = new FlagStore([flagWithRollout(100)]);
  assert.strictEqual(full.evaluate('flag-x', { id: 'u-21846' }).value, 'on'); // bucket 9999
});

test('boundary: explanation reports bucket and bucket key', () => {
  const store = new FlagStore([flagWithRollout(50, 'saltA')]);
  const r = store.evaluate('flag-x', { id: 'u-3185' });
  assert.strictEqual(r.bucketKey, 'saltA:flag-x:u-3185');
  assert.strictEqual(r.bucket, 5000);
  assert.strictEqual(r.value, 'off'); // 5000 is not < 5000
  assert.strictEqual(r.reason, 'default');
});

test('boundary: salt rehashes the same user into a different bucket', () => {
  const plain = new FlagStore([flagWithRollout(50)]);
  const salted = new FlagStore([flagWithRollout(50, 'saltA')]);
  const a = plain.evaluate('flag-x', { id: 'u-3185' });
  const b = salted.evaluate('flag-x', { id: 'u-3185' });
  assert.notStrictEqual(a.bucket, b.bucket);
});
