import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mergeObligations, isCommutative } from '../src/merge.ts';
import { isSubsetMinimalCore } from '../src/core.ts';
import { solveLimitObligations, constraintsOf, solveConstraints } from '../src/limits.ts';
import type {
  LimitObligation,
  MaskObligation,
  AuditTagsObligation,
  OrderedStepsObligation,
  OutputFormatObligation,
  Obligation,
} from '../src/obligations.ts';

test('limits merge to the stricter value', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'l1', name: 'daily', bound: 'upper', value: 10000 },
    { kind: 'limit', id: 'l2', name: 'daily', bound: 'upper', value: 5000 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.equal(out.merged.limits.daily.value, 5000);
  assert.deepEqual(
    out.merged.limits.daily.evidence.map((e) => e.source),
    ['l2'],
  );
});

test('lower-bound limits merge to the stricter (larger) floor', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'l1', name: 'reviewers', bound: 'lower', value: 2 },
    { kind: 'limit', id: 'l2', name: 'reviewers', bound: 'lower', value: 3 },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.equal(out.merged.limits.reviewers.value, 3);
});

test('masked fields merge as union with provenance', () => {
  const obs: MaskObligation[] = [
    { kind: 'mask', id: 'm1', fields: ['ssn', 'dob'] },
    { kind: 'mask', id: 'm2', fields: ['dob', 'address'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.deepEqual(out.merged.maskFields.fields, ['address', 'dob', 'ssn']);
  assert.deepEqual(out.merged.maskFields.evidence.dob, ['m1', 'm2']);
});

test('audit tags merge as union (order-independent)', () => {
  const obs: AuditTagsObligation[] = [
    { kind: 'auditTags', id: 'a1', tags: ['export', 'gdpr'] },
    { kind: 'auditTags', id: 'a2', tags: ['gdpr', 'finance'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.deepEqual(out.merged.auditTags.tags, ['export', 'finance', 'gdpr']);
  assert.deepEqual(out.merged.auditTags.evidence.gdpr, ['a1', 'a2']);
});

test('order-independent kinds are commutative', () => {
  const obs: Obligation[] = [
    { kind: 'limit', id: 'l1', name: 'daily', bound: 'upper', value: 10000 },
    { kind: 'limit', id: 'l2', name: 'daily', bound: 'upper', value: 5000 },
    { kind: 'mask', id: 'm1', fields: ['ssn'] },
    { kind: 'mask', id: 'm2', fields: ['dob'] },
    { kind: 'auditTags', id: 'a1', tags: ['x'] },
    { kind: 'auditTags', id: 'a2', tags: ['y'] },
    { kind: 'outputFormat', id: 'f1', allowed: ['csv', 'json'] },
    { kind: 'outputFormat', id: 'f2', allowed: ['json', 'parquet'] },
  ];
  const byKind = new Map<string, Obligation[]>();
  for (const o of obs) byKind.set(o.kind, [...(byKind.get(o.kind) ?? []), o]);
  for (const group of byKind.values()) {
    assert.equal(isCommutative(group), true);
  }
});

test('ordered steps preserve each obligation\'s internal order', () => {
  const obs: OrderedStepsObligation[] = [
    { kind: 'orderedSteps', id: 's1', steps: ['redact', 'watermark', 'sign'] },
    { kind: 'orderedSteps', id: 's2', steps: ['scan', 'redact'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  const idx = (s: string) => out.merged.orderedSteps.steps.indexOf(s);
  assert.ok(idx('redact') < idx('watermark'));
  assert.ok(idx('watermark') < idx('sign'));
  assert.ok(idx('scan') < idx('redact'));
  assert.deepEqual(out.merged.orderedSteps.evidence.redact, ['s1', 's2']);
});

test('ordered steps are order-sensitive: input order breaks ties explicitly', () => {
  const a: OrderedStepsObligation = { kind: 'orderedSteps', id: 's1', steps: ['alpha', 'beta'] };
  const b: OrderedStepsObligation = { kind: 'orderedSteps', id: 's2', steps: ['gamma', 'delta'] };
  const ab = mergeObligations([a, b]);
  const ba = mergeObligations([b, a]);
  assert.ok(ab.ok && ba.ok);
  assert.deepEqual(ab.merged.orderedSteps.steps, ['alpha', 'beta', 'gamma', 'delta']);
  assert.deepEqual(ba.merged.orderedSteps.steps, ['gamma', 'delta', 'alpha', 'beta']);
  assert.throws(() => isCommutative([a, b]), /order-sensitive/);
});

test('cyclic step ordering fails with a minimal core', () => {
  const obs: OrderedStepsObligation[] = [
    { kind: 'orderedSteps', id: 's1', steps: ['a', 'b'] },
    { kind: 'orderedSteps', id: 's2', steps: ['b', 'a'] },
    { kind: 'orderedSteps', id: 's3', steps: ['c', 'd'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(!out.ok);
  assert.equal(out.conflict.kind, 'orderedSteps');
  assert.deepEqual(out.conflict.core.map((o) => o.id).sort(), ['s1', 's2']);
});

test('mutually exclusive output formats fail explicitly, never silently picked', () => {
  const obs: OutputFormatObligation[] = [
    { kind: 'outputFormat', id: 'f1', allowed: ['csv'] },
    { kind: 'outputFormat', id: 'f2', allowed: ['xlsx'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(!out.ok);
  assert.equal(out.conflict.kind, 'outputFormat');
  assert.deepEqual(out.conflict.core.map((o) => o.id).sort(), ['f1', 'f2']);
});

test('compatible output formats intersect', () => {
  const obs: OutputFormatObligation[] = [
    { kind: 'outputFormat', id: 'f1', allowed: ['csv', 'json'] },
    { kind: 'outputFormat', id: 'f2', allowed: ['json', 'parquet'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  assert.deepEqual(out.merged.outputFormat.allowed, ['json']);
  assert.deepEqual(out.merged.outputFormat.evidence.json, ['f1', 'f2']);
});

test('unsatisfiable limits return a subset-minimal core', () => {
  const obs: LimitObligation[] = [
    { kind: 'limit', id: 'l1', name: 'x', bound: 'lower', value: 10 },
    { kind: 'limit', id: 'l2', name: 'x', bound: 'upper', value: 5 },
    { kind: 'limit', id: 'l3', name: 'y', bound: 'upper', value: 3 },
  ];
  const out = mergeObligations(obs);
  assert.ok(!out.ok);
  assert.equal(out.conflict.kind, 'limit');
  const coreIds = out.conflict.core.map((o) => o.id).sort();
  assert.deepEqual(coreIds, ['l1', 'l2']);
  const core = out.conflict.core as LimitObligation[];
  assert.ok(
    isSubsetMinimalCore(core, (subset) => !solveConstraints(subset.flatMap(constraintsOf)).ok),
  );
});

test('nothing is silently dropped: every obligation id appears in evidence', () => {
  const obs: Obligation[] = [
    { kind: 'limit', id: 'l1', name: 'daily', bound: 'upper', value: 10000 },
    { kind: 'limit', id: 'l2', name: 'daily', bound: 'upper', value: 5000 },
    { kind: 'mask', id: 'm1', fields: ['ssn'] },
    { kind: 'auditTags', id: 'a1', tags: ['export'] },
    { kind: 'orderedSteps', id: 's1', steps: ['redact', 'sign'] },
    { kind: 'outputFormat', id: 'f1', allowed: ['json'] },
  ];
  const out = mergeObligations(obs);
  assert.ok(out.ok);
  const cited = new Set<string>();
  for (const l of Object.values(out.merged.limits)) for (const e of l.evidence) cited.add(e.source);
  for (const ids of Object.values(out.merged.maskFields.evidence)) ids.forEach((i) => cited.add(i));
  for (const ids of Object.values(out.merged.auditTags.evidence)) ids.forEach((i) => cited.add(i));
  for (const ids of Object.values(out.merged.orderedSteps.evidence)) ids.forEach((i) => cited.add(i));
  for (const ids of Object.values(out.merged.outputFormat.evidence)) ids.forEach((i) => cited.add(i));
  // l1 is dominated by l2 (stricter), so it need not appear in limit evidence;
  // every other obligation must be cited.
  for (const id of ['l2', 'm1', 'a1', 's1', 'f1']) assert.ok(cited.has(id), `missing ${id}`);
  assert.ok(solveLimitObligations(obs.filter((o): o is LimitObligation => o.kind === 'limit')).ok);
});
