import { test } from 'node:test';
import assert from 'node:assert/strict';
import { repairAllocation } from '../src/index.ts';

const items9_1 = [
  { id: 'a', weight: 9n },
  { id: 'b', weight: 1n },
] as const;

test('weight correction reaches the new optimum with minimal moves', () => {
  const r = repairAllocation({ total: 10n, items: [...items9_1] }, { a: 5n, b: 5n });
  assert.equal(r.status, 'ok');
  assert.deepEqual(r.allocation, { a: 9n, b: 1n });
  assert.equal(r.moves, 8n);
  assert.equal(r.pending, 0n);
  assert.equal(r.deviation, 0n);
  assert.equal(r.certificates.conservation.ok, true);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('no needless churn: already-optimal previous allocation is kept', () => {
  const r = repairAllocation(
    { total: 10n, items: [{ id: 'a', weight: 1n }, { id: 'b', weight: 1n }] },
    { a: 5n, b: 5n },
  );
  assert.deepEqual(r.allocation, { a: 5n, b: 5n });
  assert.equal(r.moves, 0n);
});

test('move budget limits the shift toward the new ideal', () => {
  const r = repairAllocation({ total: 10n, items: [...items9_1] }, { a: 5n, b: 5n }, { moveBudget: 4n });
  assert.equal(r.status, 'ok');
  assert.deepEqual(r.allocation, { a: 7n, b: 3n });
  assert.equal(r.moves, 4n);
  assert.equal(r.deviation, 40n);
  assert.ok(r.moves <= 4n);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('insufficient budget leaves the shortfall pending instead of violating conservation', () => {
  const r = repairAllocation({ total: 14n, items: [...items9_1] }, { a: 5n, b: 5n }, { moveBudget: 2n });
  assert.equal(r.status, 'pending');
  assert.equal(r.pending, 2n);
  assert.deepEqual(r.allocation, { a: 7n, b: 5n });
  assert.equal(r.moves, 2n);
  const sum = r.allocation.a + r.allocation.b + r.pending;
  assert.equal(sum, 14n);
  assert.equal(r.certificates.conservation.ok, true);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('among equal optima the fewest-moves allocation wins, ties by stable id', () => {
  const r = repairAllocation(
    {
      total: 10n,
      items: [
        { id: 'a', weight: 1n },
        { id: 'b', weight: 1n },
        { id: 'c', weight: 1n },
        { id: 'd', weight: 1n },
      ],
    },
    { a: 2n, b: 2n, c: 2n, d: 4n },
  );
  // dev-optimal: two 3s and two 2s; min moves 2 (drop d by 1, raise one);
  // a/b/c symmetric -> stable-id tie-break raises 'a'.
  assert.deepEqual(r.allocation, { a: 3n, b: 2n, c: 2n, d: 3n });
  assert.equal(r.moves, 2n);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('repair respects overlapping groups jointly (no group-by-group breakage)', () => {
  const r = repairAllocation(
    {
      total: 6n,
      items: [
        { id: 'a', weight: 1n },
        { id: 'b', weight: 1n },
        { id: 'c', weight: 1n },
      ],
      groups: [
        { id: 'g1', members: ['a', 'b'], min: 5n },
        { id: 'g2', members: ['b', 'c'], max: 3n },
      ],
    },
    { a: 2n, b: 2n, c: 2n },
  );
  assert.equal(r.status, 'ok');
  const { a, b, c } = r.allocation;
  assert.equal(a + b + c, 6n);
  assert.ok(a + b >= 5n, 'g1 min');
  assert.ok(b + c <= 3n, 'g2 max');
  assert.equal(r.certificates.conservation.ok, true);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('unknown previous id and negative budget are rejected', () => {
  assert.throws(
    () => repairAllocation({ total: 1n, items: [{ id: 'a', weight: 1n }] }, { ghost: 1n }),
    /unknown id/,
  );
  assert.throws(
    () => repairAllocation({ total: 1n, items: [{ id: 'a', weight: 1n }] }, { a: 0n }, { moveBudget: -1n }),
    /non-negative/,
  );
});
