import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeProblem,
  proveOptimalityByEnumeration,
  solveApportion,
  verifyConservation,
  verifyLowerBoundCertificate,
} from '../src/index.ts';

const problem = {
  total: 6n,
  items: [
    { id: 'a', weight: 1n },
    { id: 'b', weight: 1n },
    { id: 'c', weight: 1n },
  ],
  groups: [{ id: 'g1', members: ['a', 'b'], min: 5n }],
} as const;

test('conservation certificate accepts the solver result and rejects tampering', () => {
  const norm = normalizeProblem(problem);
  const r = solveApportion(problem);
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  assert.deepEqual(verifyConservation(norm, r.allocation), { ok: true, violations: [] });

  const tamperedSum = { ...r.allocation, c: r.allocation.c + 1n };
  const bad1 = verifyConservation(norm, tamperedSum);
  assert.equal(bad1.ok, false);
  assert.ok(bad1.violations.some((v) => v.includes('conservation')));

  const tamperedGroup = { a: 1n, b: 1n, c: 4n };
  const bad2 = verifyConservation(norm, tamperedGroup);
  assert.equal(bad2.ok, false);
  assert.ok(bad2.violations.some((v) => v.includes('g1')));

  const tamperedBound = { a: 3n, b: 2n, c: 1n, ghost: 0n };
  const bad3 = verifyConservation(norm, tamperedBound);
  assert.equal(bad3.ok, false);
  assert.ok(bad3.violations.some((v) => v.includes('unknown item')));
});

test('lower-bound certificate verification catches a forged bound', () => {
  const norm = normalizeProblem(problem);
  const r = solveApportion(problem);
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  const cert = r.certificates.lowerBound;
  assert.deepEqual(verifyLowerBoundCertificate(norm, cert), { ok: true, violations: [] });
  const forged = { ...cert, lowerBound: cert.lowerBound + 1n };
  const bad = verifyLowerBoundCertificate(norm, forged);
  assert.equal(bad.ok, false);
});

test('enumeration proof independently confirms optimality and refutes worse claims', () => {
  const norm = normalizeProblem(problem);
  const r = solveApportion(problem);
  assert.equal(r.status, 'ok');
  if (r.status !== 'ok') return;
  const proof = proveOptimalityByEnumeration(norm, { mode: 'apportion' }, [0n, r.deviation, 0n], 200_000);
  assert.equal(proof.exhausted, true);
  assert.equal(proof.proven, true);

  const worseClaim = proveOptimalityByEnumeration(norm, { mode: 'apportion' }, [0n, r.deviation + 2n, 0n], 200_000);
  assert.equal(worseClaim.proven, false);
  assert.ok(worseClaim.bestCost !== null && worseClaim.bestCost[1] === r.deviation);
});

test('enumeration proof declines to claim when the space exceeds the limit', () => {
  const norm = normalizeProblem({
    total: 100n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
  });
  const proof = proveOptimalityByEnumeration(norm, { mode: 'apportion' }, [0n, 0n, 0n], 10);
  assert.equal(proof.exhausted, false);
  assert.equal(proof.proven, false);
});
