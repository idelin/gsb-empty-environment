import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { branch, docFromPaths, leaf, nil, tomb } from '../src/dsl.ts';
import { exhaustiveStateTruth, truthOracle, type FlatState } from '../src/truth/oracle.ts';
import type { Marker } from '../src/dsl.ts';

function markerFor(state: FlatState): Marker {
  if (state.t === 'value') return leaf(state.v);
  if (state.t === 'null') return nil();
  return tomb();
}

test('exhaustive 4^3 state combinations match the truth interpreter', () => {
  const cases = exhaustiveStateTruth(3);
  assert.equal(cases.length, 64);

  for (const kase of cases) {
    const branches = kase.opinions.map((opinion) =>
      branch(
        opinion.branch,
        opinion.priority,
        docFromPaths(`doc-${opinion.branch}-${kase.index}`, {
          ...(opinion.states.k ? { k: markerFor(opinion.states.k) } : {}),
        }),
      ),
    );

    const result = acceptMerge({
      branches,
      grants: [{ issuer: '*', path: '/**', facets: '*' }],
      options: { requireSignatureEvidence: false },
    });

    assert.ok(result.ok, `open merge should accept case ${kase.index}: ${JSON.stringify(result.ok ? null : result.errors)}`);
    if (!result.ok) continue;

    const truth = kase.truth.k;
    if (truth.kind === 'value') {
      assert.deepEqual(result.tree, { k: 'x' }, `case ${kase.index}`);
    } else if (truth.kind === 'null') {
      assert.deepEqual(result.tree, { k: null }, `case ${kase.index}`);
      assert.ok(Object.prototype.hasOwnProperty.call(result.tree, 'k'), 'null key must remain present');
    } else {
      assert.deepEqual(result.tree, {}, `case ${kase.index} should have no k`);
    }

    if (truth.sourceBranch && (truth.kind === 'value' || truth.kind === 'null')) {
      const prov = result.provenance.find((p) => p.pointer === '/k');
      assert.ok(prov, `provenance /k for case ${kase.index}`);
      assert.equal(prov?.valueDecision.branchId, truth.sourceBranch, `source branch case ${kase.index}`);
    }
  }
});

test('oracle: retracting an override exposes the lower branch value', () => {
  const truth = truthOracle({
    paths: ['k'],
    opinions: [
      { branch: 'base', priority: 10, states: { k: { t: 'value', v: 'classic' } } },
      { branch: 'over', priority: 20, states: { k: { t: 'value', v: 'dark' } } },
    ],
    retractions: [{ path: 'k', branch: 'over' }],
  });
  assert.equal(truth.k.kind, 'value');
  assert.equal(truth.k.value, 'classic');
  assert.equal(truth.k.sourceBranch, 'base');
  assert.equal(truth.k.exposed, true);
  assert.equal(truth.k.deletedBranch, 'over');

  const result = acceptMerge({
    branches: [
      branch('base', 10, docFromPaths('d-base', { k: leaf('classic') }, { attribution: { issuer: 'b' } })),
      branch('over', 20, docFromPaths('d-over', { k: leaf('dark') }, { attribution: { issuer: 'o' } })),
    ],
    deletions: [{ path: '/k', branchId: 'over', issuer: 'admin' }],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  });
  assert.ok(result.ok);
  if (result.ok) {
    assert.deepEqual(result.tree, { k: 'classic' });
    assert.equal(result.provenance[0].exposures[0]?.exposedBranchId, 'base');
  }
});
