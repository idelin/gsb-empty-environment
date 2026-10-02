import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solve } from '../src/solver.ts';

test('item upper bound: deviation minimized exactly', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n, upper: 2n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.deepEqual(r.allocation, { a: 4n, b: 4n, c: 2n });
  // ideal 10/3 each; deviation = 2/3 + 2/3 + 4/3 = 8/3
  assert.equal(r.objective.deviation.toString(), '8/3');
});

test('overlapping group lowers are solved jointly (no per-group greedy)', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [
      { id: 'g1', members: ['a', 'b'], lower: 7n },
      { id: 'g2', members: ['b', 'c'], lower: 7n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // a+b >= 7 and b+c >= 7 with a+b+c = 10 force b >= 4
  assert.equal(r.allocation.a + r.allocation.b >= 7n, true);
  assert.equal(r.allocation.b + r.allocation.c >= 7n, true);
  // deviation-optimal: b = 4, a = c = 3 -> deviation 4/3
  assert.deepEqual(r.allocation, { a: 3n, b: 4n, c: 3n });
  assert.equal(r.objective.deviation.toString(), '4/3');
});

test('group upper bound is respected', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [{ id: 'g', members: ['a', 'b'], upper: 3n }],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.allocation.a + r.allocation.b <= 3n, true);
  assert.equal(r.allocation.a + r.allocation.b + r.allocation.c, 10n);
});

test('equal remainders: the leftover unit goes to the smaller stable id', () => {
  const r = solve({
    total: 3n,
    items: [
      { id: 'b', weight: 1n },
      { id: 'a', weight: 1n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.deepEqual(r.allocation, { a: 2n, b: 1n });
});

test('constrained ties resolve deterministically by id order', () => {
  const r = solve({
    total: 3n,
    items: [
      { id: 'b', weight: 1n, upper: 1n },
      { id: 'a', weight: 1n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // b capped at 1 -> a = 2 forced
  assert.deepEqual(r.allocation, { a: 2n, b: 1n });
});

test('infeasible lower bounds: deficit reported, nothing forced', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n, lower: 5n },
      { id: 'b', weight: 1n, lower: 5n },
      { id: 'c', weight: 1n, lower: 4n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.deficit, 4n); // needs 14, has 10
  assert.equal(r.pending, 4n); // shortfall stays pending
  assert.equal('allocation' in r, false);
  assert.equal(r.violations.length > 0, true);
});

test('infeasible overlapping groups: conflict reported, not forced', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n, lower: 5n },
    ],
    groups: [{ id: 'g', members: ['a', 'b'], lower: 8n }],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  // c keeps 5, so at most 5 can reach {a,b} but 8 is required
  assert.equal(r.deficit, 3n);
  assert.equal(r.pending, 3n);
});

test('upper bounds below total: infeasible with explicit violation', () => {
  const r = solve({
    total: 10n,
    items: [
      { id: 'a', weight: 1n, upper: 3n },
      { id: 'b', weight: 1n, upper: 3n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.violations.some((v) => v.includes('at most 6')), true);
});

test('negative weight is rejected', () => {
  const r = solve({
    total: 5n,
    items: [
      { id: 'a', weight: '-3/4' },
      { id: 'b', weight: 2n },
    ],
  });
  assert.equal(r.status, 'error');
  if (r.status === 'error') assert.match(r.error, /negative weight/);
});

test('rational weights distribute exactly with conservation', () => {
  const r = solve({
    total: 100n,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/4' },
      { id: 'c', weight: '5/12' },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // ideals: 400/12=100/3, 300/12=25, 500/12=125/3 -> floors 33,25,41 leftover 1
  // remainders 1/3, 0, 2/3 -> c gets the unit
  assert.deepEqual(r.allocation, { a: 33n, b: 25n, c: 42n });
  assert.equal(r.allocation.a + r.allocation.b + r.allocation.c, 100n);
});

test('huge totals stay exact end-to-end', () => {
  const total = 10n ** 40n + 1n;
  const r = solve({
    total,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  const sum = r.allocation.a + r.allocation.b + r.allocation.c;
  assert.equal(sum, total);
  // total = 10^40 + 1 == 2 (mod 3): ideal = base + 2/3, leftover 2 units
  const base = (total - 2n) / 3n;
  const counts = [r.allocation.a, r.allocation.b, r.allocation.c].sort();
  assert.deepEqual(counts, [base, base + 1n, base + 1n]);
});
