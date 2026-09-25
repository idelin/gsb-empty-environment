import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mergeObligations } from '../src/merge.ts';
import { isSubsetMinimalCore } from '../src/core.ts';
import { constraintsOf, solveConstraints } from '../src/limits.ts';
import { referenceCheckCore } from '../src/reference.ts';
import type { LimitObligation } from '../src/obligations.ts';

test('dependent limits: perTx capped by daily through a dependency relation', () => {
  const obs: LimitObligation[] = [
    {
      kind: 'limit', id: 'ob-per-tx', name: 'perTx', bound: 'upper', value: 8000,
      requires: [{ left: 'perTx', rel: '<=', right: 'daily' }],
    },
    { kind: 'limit', id: 'ob-daily', name: 'daily', bound: 'upper', value: 5000 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  // perTx <= 8000 and perTx <= daily <= 5000  =>  strictest feasible cap is 5000
  assert.equal(out.merged.limits.perTx.value, 5000);
  assert.equal(out.merged.limits.daily.value, 5000);
  // provenance: the perTx bound is derived through both obligations
  const sources = out.merged.limits.perTx.evidence.map((e) => e.source);
  assert.ok(sources.includes('ob-per-tx'));
  assert.ok(sources.includes('ob-daily'));
});

test('dependency with offset propagates constants', () => {
  const obs: LimitObligation[] = [
    {
      kind: 'limit', id: 'ob-batch', name: 'batch', bound: 'upper', value: 100,
      requires: [{ left: 'batch', rel: '>=', right: 'single', offset: 10 }],
    },
    { kind: 'limit', id: 'ob-single', name: 'single', bound: 'upper', value: 25 },
    { kind: 'limit', id: 'ob-single-floor', name: 'single', bound: 'lower', value: 15 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  // single in [15, 25] and batch >= single + 10 and batch <= 100
  assert.equal(out.merged.limits.single.value, 25);
  assert.equal(out.merged.limits.single.lowerBound, 15);
  assert.equal(out.merged.limits.batch.value, 100);
  assert.equal(out.merged.limits.batch.lowerBound, 25);
});

test('lower bounds take the stricter (larger) value', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'ob-a', name: 'auditors', bound: 'lower', value: 2 },
    { kind: 'limit', id: 'ob-b', name: 'auditors', bound: 'lower', value: 3 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.equal(out.merged.limits.auditors.value, 3);
});

test('infeasible limits fail with a subset-minimal core, verified by enumeration', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'ob-floor', name: 'x', bound: 'lower', value: 10 },
    { kind: 'limit', id: 'ob-cap', name: 'x', bound: 'upper', value: 5 },
    { kind: 'limit', id: 'ob-other', name: 'y', bound: 'upper', value: 3 },
  ];
  const out = mergeObligations(obs);
  assert.ok(!out.ok);
  assert.equal(out.conflict.kind, 'limit');
  const core = out.conflict.core as LimitObligation[];
  assert.deepEqual(core.map((o) => o.id).sort(), ['ob-cap', 'ob-floor']);
  // subset-minimal: removing any element restores satisfiability
  assert.ok(
    isSubsetMinimalCore(core, (subset) => !solveConstraints(subset.flatMap(constraintsOf)).ok),
  );
  // independently confirmed by finite enumeration
  assert.ok(referenceCheckCore(core));
});

test('cyclic dependencies are infeasible and produce a minimal core', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'ob-1', name: 'a', bound: 'upper', value: 10,
      requires: [{ left: 'a', rel: '>=', right: 'b', offset: 2 }] },
    { kind: 'limit', id: 'ob-2', name: 'b', bound: 'upper', value: 10,
      requires: [{ left: 'b', rel: '>=', right: 'a', offset: 2 }] },
  ];
  const out = mergeObligations(obs);
  assert.ok(!out.ok);
  assert.equal(out.conflict.kind, 'limit');
  assert.deepEqual(out.conflict.core.map((o) => o.id).sort(), ['ob-1', 'ob-2']);
});

test('equality dependency ties two limits together', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'ob-x', name: 'x', bound: 'upper', value: 7,
      requires: [{ left: 'x', rel: '==', right: 'y' }] },
    { kind: 'limit', id: 'ob-y', name: 'y', bound: 'upper', value: 9 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.equal(out.merged.limits.x.value, 7);
  assert.equal(out.merged.limits.y.value, 7);
});
