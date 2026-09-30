import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { loadInstance } from '../src/model.ts';
import { solve } from '../src/solve.ts';
import { issueCertificate, verifyCertificate } from '../src/certificate.ts';

const demoPath = new URL('../samples/demo.json', import.meta.url);
const demoRaw = JSON.parse(readFileSync(demoPath, 'utf8'));
const demo = loadInstance(demoRaw);

test('certificate for optimal solution verifies, optimality independently confirmed', () => {
  const cert = issueCertificate(demo, solve(demo));
  const v = verifyCertificate(demo, cert);
  assert.equal(v.valid, true);
  assert.equal(v.stale, false);
  assert.equal(v.optimalityProof, 'exhaustive-confirmed');
  assert.ok(v.checks.every((c) => c.ok), v.checks.filter((c) => !c.ok).map((c) => c.name).join(','));
});

test('objective change makes the old certificate stale (re-verification fails)', () => {
  const cert = issueCertificate(demo, solve(demo));
  // change the objective: tighten the error budget
  const changed = loadInstance({ ...demoRaw, errorBudget: '0.04' });
  const v = verifyCertificate(changed, cert);
  assert.equal(v.stale, true);
  assert.equal(v.valid, false);
  assert.ok(v.checks.some((c) => c.name === 'instance-hash' && !c.ok));
});

test('tampered selection is rejected', () => {
  const cert = issueCertificate(demo, solve(demo));
  const forged = {
    ...cert,
    selection: { ...cert.selection!, sensor: 'low' }, // cheaper than certified
    upperBoundCost: '23',
  };
  const v = verifyCertificate(demo, forged);
  assert.equal(v.valid, false);
  assert.ok(v.checks.some((c) => !c.ok));
});

test('tampered error report is rejected', () => {
  const cert = issueCertificate(demo, solve(demo));
  const forged = { ...cert, errorTotal: '1/1000000' };
  const v = verifyCertificate(demo, forged);
  assert.equal(v.valid, false);
  assert.ok(v.checks.some((c) => c.name === 'error-matches-certificate' && !c.ok));
});

test('budget-exhausted certificate: verifies as undecided, claims no optimality', () => {
  const cert = issueCertificate(demo, solve(demo, { nodeBudget: 1 }));
  assert.equal(cert.status, 'undecided');
  assert.equal(cert.lowerBoundCost, '23');
  const v = verifyCertificate(demo, cert);
  assert.equal(v.valid, true);
  assert.equal(v.optimalityProof, 'not-claimed');
  assert.ok(v.checks.some((c) => c.name === 'no-optimality-claim' && c.ok));
});

test('infeasible certificate verifies', () => {
  const impossible = loadInstance({
    name: 'impossible',
    errorBudget: '0.0001',
    sources: [{ id: 'n', kind: 'independent' }],
    components: [
      {
        id: 'x',
        tiers: [{ id: 't1', cost: '1', error: '0.5' }],
        sensitivities: { n: '1' },
      },
    ],
  });
  const cert = issueCertificate(impossible, solve(impossible));
  assert.equal(cert.status, 'infeasible');
  assert.equal(cert.lowerBoundCost, null);
  const v = verifyCertificate(impossible, cert);
  assert.equal(v.valid, true);
});
