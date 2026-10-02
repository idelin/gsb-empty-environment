import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solve } from '../src/solver.ts';
import { verifyCertificate } from '../src/certificates.ts';
import type { SolveInput } from '../src/model.ts';

const constrainedInput: SolveInput = {
  total: 10n,
  items: [
    { id: 'a', weight: 1n },
    { id: 'b', weight: 2n },
    { id: 'c', weight: 1n, upper: 2n },
  ],
  groups: [{ id: 'g', members: ['a', 'b'], lower: 6n }],
};

test('optimal result carries a valid conservation + lower-bound certificate', () => {
  const r = solve(constrainedInput);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.certificate.conservation.conserved, true);
  assert.equal(r.certificate.optimality.boundHolds, true);
  assert.equal(r.certificate.optimality.method, 'exhaustive-enumeration');

  const v = verifyCertificate(constrainedInput, r);
  assert.equal(
    v.checks.every((c) => c.ok),
    true,
    JSON.stringify(v.checks, null, 2),
  );
  assert.equal(v.ok, true);
});

test('independent verifier catches a tampered (non-conserving) allocation', () => {
  const r = solve(constrainedInput);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  const tampered = {
    ...r,
    allocation: { ...r.allocation, a: r.allocation.a + 1n },
  };
  const v = verifyCertificate(constrainedInput, tampered);
  assert.equal(v.ok, false);
  assert.equal(v.checks.some((c) => c.name === 'conservation' && !c.ok), true);
});

test('independent verifier catches a bound violation', () => {
  const r = solve(constrainedInput);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  // force c above its upper bound 2 while keeping the sum at 10
  const tampered = {
    ...r,
    allocation: { a: r.allocation.a - 1n, b: r.allocation.b, c: 3n },
  };
  const v = verifyCertificate(constrainedInput, tampered);
  assert.equal(v.ok, false);
  assert.equal(v.checks.some((c) => c.name === 'item-bounds' && !c.ok), true);
});

test('infeasible results verify independently too', () => {
  const input: SolveInput = {
    total: 5n,
    items: [
      { id: 'a', weight: 1n, lower: 4n },
      { id: 'b', weight: 1n, lower: 4n },
    ],
  };
  const r = solve(input);
  assert.equal(r.status, 'infeasible');
  const v = verifyCertificate(input, r);
  assert.equal(v.ok, true, JSON.stringify(v.checks, null, 2));
});

test('re-weighting certificate includes a movement lower bound', () => {
  const input: SolveInput = {
    total: 10n,
    previous: { a: 8n, b: 0n },
    items: [
      { id: 'a', weight: 1n, upper: 6n },
      { id: 'b', weight: 1n },
    ],
  };
  const r = solve(input);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.certificate.optimality.lowerBound.movement, '6');
  assert.equal(r.objective.movement, 6n);
  const v = verifyCertificate(input, r);
  assert.equal(v.ok, true, JSON.stringify(v.checks, null, 2));
});

test('certificate stays exact for huge totals (no Number transit)', () => {
  const total = 10n ** 30n - 1n; // 999...9, divisible by 3
  const input: SolveInput = {
    total,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  };
  const r = solve(input);
  assert.equal(r.status, 'optimal');
  if (r.status !== 'optimal') return;
  assert.equal(r.certificate.conservation.allocatedSum, total.toString());
  // ideal = total/3 = 333...333 is an exact integer -> deviation 0
  assert.equal(r.objective.deviation.toString(), '0');
  const v = verifyCertificate(input, r);
  assert.equal(v.ok, true, JSON.stringify(v.checks, null, 2));
});
