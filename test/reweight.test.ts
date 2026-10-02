import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reallocate } from '../src/reweight.ts';

test('re-weighting minimizes movement BEFORE deviation', () => {
  const r = reallocate({
    total: 10n,
    previous: { a: 5n, b: 5n },
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 9n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // deviation-optimal would be {a:1,b:9} (movement 8);
  // movement-optimal keeps {a:5,b:5} (movement 0)
  assert.deepEqual(r.allocation, { a: 5n, b: 5n });
  assert.equal(r.objective.movement, 0n);
  assert.equal(r.objective.deviation.toString(), '8'); // |5-1| + |5-9|
});

test('among equal movement, deviation is minimized', () => {
  const r = reallocate({
    total: 10n,
    previous: { a: 8n, b: 0n },
    items: [
      { id: 'a', weight: 1n, upper: 6n },
      { id: 'b', weight: 1n },
    ],
  });
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // a capped at 6 -> movement at least 2+4=6; {a:6,b:4} also minimizes deviation
  assert.deepEqual(r.allocation, { a: 6n, b: 4n });
  assert.equal(r.objective.movement, 6n);
  assert.equal(r.objective.deviation.toString(), '2'); // |6-5| + |4-5|
});

test('re-weighting respects overlapping groups globally', () => {
  const r = reallocate({
    total: 10n,
    previous: { a: 4n, b: 3n, c: 3n },
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
  // b >= 4 forced; previous b = 3 -> movement at least 1; {4,4,2}? g2: 4+2=6 <7 no.
  // feasible with b=4: a>=3, c>=3, a+c=6 -> {3,4,3}, movement |3-4|+|4-3|+|3-3|=2
  assert.deepEqual(r.allocation, { a: 3n, b: 4n, c: 3n });
  assert.equal(r.objective.movement, 2n);
  assert.equal(r.allocation.a + r.allocation.b >= 7n, true);
  assert.equal(r.allocation.b + r.allocation.c >= 7n, true);
});

test('insufficient budget keeps the shortfall pending (保留未决)', () => {
  const r = reallocate({
    total: 8n,
    previous: { a: 4n, b: 4n },
    items: [
      { id: 'a', weight: 1n, lower: 5n },
      { id: 'b', weight: 1n, lower: 5n },
    ],
  });
  assert.equal(r.status, 'infeasible');
  if (r.status !== 'infeasible') return;
  assert.equal(r.deficit, 2n);
  assert.equal(r.pending, 2n);
  assert.equal('allocation' in r, false);
});
