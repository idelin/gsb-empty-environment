import { test } from 'node:test';
import assert from 'node:assert/strict';
import { largestRemainder } from '../src/largestRemainder.ts';

test('total 1 split three ways: exactly one share of 1', () => {
  const r = largestRemainder(
    [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
    1n,
  );
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  const values = Object.values(r.allocation);
  assert.deepEqual(values.sort(), [0n, 0n, 1n]);
  // equal remainders -> stable id order wins: 'a' gets the unit
  assert.equal(r.allocation.a, 1n);
  assert.equal(r.allocation.b, 0n);
  assert.equal(r.allocation.c, 0n);
});

test('equal remainders break ties by stable id, not input order', () => {
  const r = largestRemainder(
    [
      { id: 'c', weight: 1n },
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
    ],
    2n,
  );
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(r.allocation, { a: 1n, b: 1n, c: 0n });
});

test('larger remainder wins the leftover unit', () => {
  const r = largestRemainder(
    [
      { id: 'a', weight: 45n }, // ideal 4.5, remainder .5
      { id: 'b', weight: 36n }, // ideal 3.6, remainder .6
      { id: 'c', weight: 19n }, // ideal 1.9, remainder .9 (largest)
    ],
    10n,
  );
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  // floors 4+3+1 = 8, leftover 2; remainders .5, .6, .9 -> c and b get them
  assert.deepEqual(r.allocation, { a: 4n, b: 4n, c: 2n });
});

test('conservation holds exactly for huge totals', () => {
  const total = 10n ** 30n + 7n;
  const r = largestRemainder(
    [
      { id: 'x', weight: '1/7' },
      { id: 'y', weight: '2/7' },
      { id: 'z', weight: '4/7' },
    ],
    total,
  );
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  const sum = r.allocation.x + r.allocation.y + r.allocation.z;
  assert.equal(sum, total);
  // ideal shares are integers + k/7; check exactness of one share
  // total/7 = 142857142857142857142857142858 + 1/7 -> x gets floor + maybe unit
  assert.equal(
    r.allocation.x >= 142857142857142857142857142858n &&
      r.allocation.x <= 142857142857142857142857142859n,
    true,
  );
});

test('negative weight is rejected', () => {
  const r = largestRemainder(
    [
      { id: 'a', weight: '-1/2' },
      { id: 'b', weight: 1n },
    ],
    5n,
  );
  assert.equal(r.status, 'error');
  if (r.status === 'error') assert.match(r.error, /negative weight/);
});

test('all-zero weights are rejected', () => {
  const r = largestRemainder(
    [
      { id: 'a', weight: 0n },
      { id: 'b', weight: 0n },
    ],
    5n,
  );
  assert.equal(r.status, 'error');
});
