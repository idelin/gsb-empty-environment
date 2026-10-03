import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solveApportion, verifyLowerBoundCertificate, normalizeProblem } from '../src/index.ts';

test('upper bound caps the largest-remainder outcome', () => {
  const r = solveApportion({
    total: 4n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n, upper: 1n },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(r.allocation, { a: 3n, b: 1n });
  assert.equal(r.deviation, 4n); // |3*2-4| + |1*2-4|
  assert.equal(r.certificates.conservation.ok, true);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('lower bound forces allocation onto a zero-weight item', () => {
  const r = solveApportion({
    total: 2n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 0n, lower: 1n },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(r.allocation, { a: 1n, b: 1n });
});

test('overlapping groups are enforced jointly, zero-deviation when possible', () => {
  const r = solveApportion({
    total: 6n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [
      { id: 'g1', members: ['a', 'b'], min: 4n },
      { id: 'g2', members: ['b', 'c'], min: 4n },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(r.allocation, { a: 2n, b: 2n, c: 2n });
  assert.equal(r.deviation, 0n);
  assert.equal(r.certificates.conservation.ok, true);
});

test('constraints first, then minimal total deviation from ideal shares', () => {
  const r = solveApportion({
    total: 6n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [{ id: 'g1', members: ['a', 'b'], min: 5n }],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  // baseline would be 2/2/2 (deviation 0) but violates g1; best feasible
  // deviation is 6, tie between (3,2,1) and (2,3,1) -> stable-id tie-break.
  assert.deepEqual(r.allocation, { a: 3n, b: 2n, c: 1n });
  assert.deepEqual(r.baseline, { a: 2n, b: 2n, c: 2n });
  assert.equal(r.deviation, 6n);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('joint solver handles groups a sequential pass would break', () => {
  const r = solveApportion({
    total: 10n,
    items: [
      { id: 'a', weight: 8n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [
      { id: 'cap', members: ['a', 'b'], max: 5n },
      { id: 'floor', members: ['b', 'c'], min: 3n },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(r.allocation, { a: 5n, b: 0n, c: 5n });
  assert.equal(r.deviation, 80n);
  assert.equal(r.certificates.conservation.ok, true);
  assert.equal(r.certificates.enumeration?.proven, true);
});

test('infeasible upper bounds return the deficit, never force-fit', () => {
  const r = solveApportion({
    total: 5n,
    items: [
      { id: 'a', weight: 1n, upper: 1n },
      { id: 'b', weight: 1n, upper: 1n },
      { id: 'c', weight: 1n, upper: 1n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.deficit, 2n);
  assert.equal('allocation' in r, false);
});

test('lower bounds above total return the surplus', () => {
  const r = solveApportion({
    total: 6n,
    items: [
      { id: 'a', weight: 1n, lower: 4n },
      { id: 'b', weight: 1n, lower: 4n },
      { id: 'c', weight: 1n, lower: 4n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.surplus, 6n);
});

test('group infeasibility reports the shortfall, not a forced allocation', () => {
  const r = solveApportion({
    total: 5n,
    items: [
      { id: 'a', weight: 1n, upper: 2n },
      { id: 'b', weight: 1n, upper: 2n },
      { id: 'c', weight: 1n },
    ],
    groups: [{ id: 'g1', members: ['a', 'b'], min: 5n }],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.reason, 'constraints-unsatisfiable');
  assert.ok(r.details.some((d) => d.includes('g1') && d.includes('shortfall 1')));
});

test('per-item lower > upper is infeasible with a clear detail', () => {
  const r = solveApportion({
    total: 3n,
    items: [
      { id: 'a', weight: 1n, lower: 3n, upper: 1n },
      { id: 'b', weight: 1n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.ok(r.details.some((d) => d.includes('lower 3 exceeds upper 1')));
});

test('unconstrained optimum matches the closed-form lower bound (certified optimal)', () => {
  const r = solveApportion({
    total: 10n,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.equal(r.deviation, 4n);
  assert.equal(r.certificates.lowerBound.lowerBound, 4n);
  assert.equal(r.certificates.lowerBound.optimal, true);
  const norm = normalizeProblem({
    total: 10n,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  });
  assert.deepEqual(verifyLowerBoundCertificate(norm, r.certificates.lowerBound), {
    ok: true,
    violations: [],
  });
});

test('exact solver stays exact at 10^18 scale (no Number anywhere)', () => {
  const total = 10n ** 18n + 1n;
  const r = solveApportion({
    total,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
  });
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  const f = (total - 2n) / 3n;
  assert.deepEqual(r.allocation, { a: f + 1n, b: f + 1n, c: f });
  assert.equal(r.deviation, 4n);
  assert.equal(r.certificates.conservation.ok, true);
});
