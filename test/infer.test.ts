import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SchemaInference, validate, type DecisionPoint } from '../src/index.ts';

function find(report: { decisionPoints: DecisionPoint[] }, path: string, dimension: string): DecisionPoint {
  const dp = report.decisionPoints.find((d) => d.path === path && d.dimension === dimension);
  assert.ok(dp, `decision point ${path} [${dimension}] should exist`);
  return dp;
}

test('empty sample set yields unknown, not a fabricated schema', () => {
  const report = new SchemaInference().report();
  assert.equal(report.status, 'unknown');
  assert.equal(report.sampleCount, 0);
  assert.deepEqual(report.primary, {});
  assert.ok(report.unknowns.length > 0);
  assert.equal(report.verification.accepted, 0);
  assert.equal(report.verification.rejected, 0);
});

test('integer-only samples: integer and number are both candidates, integer flagged inductive', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { age: 1 });
  inf.addSample('b', { age: 5 });
  const report = inf.report();
  const dp = find(report, '/age', 'type');
  assert.equal(dp.options[0].label, 'integer');
  assert.equal(dp.options[0].inductive, true);
  assert.equal(dp.options[1].label, 'number');
  assert.equal(dp.options[2].label, 'any');
  const distinguisher = dp.options[0].distinguisher;
  assert.ok(distinguisher);
  const probe = (distinguisher.instance as { age: number }).age;
  assert.ok(!Number.isInteger(probe), 'distinguisher must be a non-integer');
  assert.equal(
    validate({ age: probe }, { type: 'object', properties: { age: { type: 'integer' } } }).length,
    1,
    'distinguisher is rejected by the integer option',
  );
  assert.equal(
    validate({ age: probe }, { type: 'object', properties: { age: { type: 'number' } } }).length,
    0,
    'distinguisher is accepted by the number option',
  );
});

test('missing and null are counted separately', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { email: 'x@y.z', nick: null });
  inf.addSample('b', { nick: 'n' });
  const report = inf.report();

  const emailPresence = find(report, '/email', 'presence');
  assert.equal(emailPresence.options[0].label, 'optional');
  assert.deepEqual(emailPresence.options[0].support, { consistent: 2, outOf: 2 });
  assert.equal(emailPresence.options[1].label, 'required');
  assert.deepEqual(emailPresence.options[1].support, { consistent: 1, outOf: 2 });

  const emailNullability = find(report, '/email', 'nullability');
  assert.equal(emailNullability.options[0].label, 'non-null');

  const nickNullability = find(report, '/nick', 'nullability');
  assert.equal(nickNullability.options[0].label, 'nullable');
  assert.deepEqual(nickNullability.options[0].support, { consistent: 2, outOf: 2 });
  assert.equal(nickNullability.options[1].label, 'non-null');
  assert.deepEqual(nickNullability.options[1].support, { consistent: 1, outOf: 2 });
});

test('required is inductive when never missing, with an omit distinguisher', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { id: 1 });
  const report = inf.report();
  const dp = find(report, '/id', 'presence');
  assert.equal(dp.options[0].label, 'required');
  assert.equal(dp.options[0].inductive, true);
  assert.deepEqual(dp.options[0].distinguisher?.instance, {});
});

test('retracting the outlier sample shrinks the range and preserves provenance counts', () => {
  const inf = new SchemaInference();
  inf.addSample('s1', { score: 10 });
  inf.addSample('s2', { score: 20 });
  inf.addSample('outlier', { score: 999 });

  let range = find(inf.report(), '/score', 'range');
  assert.equal(range.options[0].label, 'range [10, 999]');
  assert.match(range.options[0].rationale, /"outlier"/);

  assert.equal(inf.retractSample('outlier'), true);
  const report = inf.report();
  range = find(report, '/score', 'range');
  assert.equal(range.options[0].label, 'range [10, 20]');
  assert.doesNotMatch(range.options[0].rationale, /outlier/);
  assert.equal(report.sampleCount, 2);
  assert.deepEqual(report.retainedSampleIds, ['s1', 's2']);
});

test('all retained samples are independently verified against the primary schema', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { age: 3, name: 'xy' });
  inf.addSample('b', { age: 4, name: 'zzz', extra: [1, 2] });
  const report = inf.report();
  assert.equal(report.verification.accepted, 2);
  assert.equal(report.verification.rejected, 0);
  assert.equal(report.verification.errors.length, 0);
});

test('unobserved keys are not rejected (open world, not permanently forbidden)', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { age: 3 });
  const report = inf.report();
  assert.equal(validate({ age: 3, brandNew: 'field' }, report.primary).length, 0);
  assert.ok(report.guarantees.some((g) => g.includes('open-world')));
});

test('field observed only as null has unknown domain', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { x: null });
  const report = inf.report();
  assert.equal(find(report, '/x', 'type').options[0].label, 'unknown');
  assert.ok(report.unknowns.some((u) => u.startsWith('/x')));
});

test('empty array leaves item domain unknown', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { tags: [] });
  const report = inf.report();
  assert.ok(report.unknowns.some((u) => u.includes('/tags[]')));
});

test('array item types are inferred and nested in the primary schema', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { tags: ['x', 'yy'] });
  const report = inf.report();
  const tags = report.primary.properties?.tags;
  assert.equal(tags?.type, 'array');
  assert.equal(tags?.items?.type, 'string');
  assert.equal(validate({ tags: ['ok'] }, report.primary).length, 0);
  assert.equal(validate({ tags: [1] }, report.primary).length, 1);
});

test('mixed int and decimal observations force the number type', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { v: 1 });
  inf.addSample('b', { v: 1.5 });
  const report = inf.report();
  const dp = find(report, '/v', 'type');
  assert.equal(dp.options[0].label, 'number');
});

test('guarantees only claim the implemented subset; inductions are listed separately', () => {
  const inf = new SchemaInference();
  inf.addSample('a', { age: 3 });
  const report = inf.report();
  assert.ok(report.guarantees.some((g) => g.includes('implemented subset only')));
  assert.ok(report.inductions.some((i) => i.includes('/age [type] integer')));
  assert.ok(report.inductions.some((i) => i.includes('sample-inductive')));
});
