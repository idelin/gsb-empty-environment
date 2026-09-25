import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalizePolicy, PolicyConflictError } from '../src/policy.ts';
import { PolicyValidationError } from '../src/condition.ts';
import { evaluate } from '../src/engine.ts';
import type { PolicySpec } from '../src/policy.ts';

const base: PolicySpec = {
  attributes: { x: [0, 1] },
  rules: [{ id: 'r1', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: '1/2' }],
  bands: [
    { grade: 'low', gte: null },
    { grade: 'high', gte: 1 },
  ],
};

test('identical duplicate rule id is counted once', () => {
  const spec: PolicySpec = { ...base, rules: [base.rules[0], { ...base.rules[0] }] };
  const policy = normalizePolicy(spec);
  assert.equal(policy.rules.length, 1);
  const result = evaluate(policy, { x: 1 });
  assert.equal(result.status, 'definite');
  assert.equal(result.definiteOutcome!.type, 'grade');
  // weight applied exactly once, not twice
  assert.equal((result.definiteOutcome as any).score.toString(), '1/2');
});

test('conflicting duplicate rule id is rejected', () => {
  const spec: PolicySpec = {
    ...base,
    rules: [base.rules[0], { ...base.rules[0], weight: '3/4' }],
  };
  assert.throws(() => normalizePolicy(spec), PolicyConflictError);
  const spec2: PolicySpec = {
    ...base,
    rules: [base.rules[0], { ...base.rules[0], hardReject: true }],
  };
  assert.throws(() => normalizePolicy(spec2), /conflicting/);
});

test('conditions may only read declared attributes', () => {
  const spec: PolicySpec = {
    ...base,
    rules: [{ id: 'bad', when: { kind: 'cmp', attr: 'ghost', op: 'eq', value: 1 } }],
  };
  assert.throws(() => normalizePolicy(spec), PolicyValidationError);
});

test('conditions are limited to integer values and finite sets', () => {
  const nonInteger: PolicySpec = {
    ...base,
    rules: [{ id: 'bad', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 0.5 } }],
  };
  assert.throws(() => normalizePolicy(nonInteger), /integer/);
  const emptySet: PolicySpec = {
    ...base,
    rules: [{ id: 'bad', when: { kind: 'in', attr: 'x', set: [] } }],
  };
  assert.throws(() => normalizePolicy(emptySet), /finite set/);
});

test('bands must be strictly increasing with at most one open lower bound', () => {
  assert.throws(
    () => normalizePolicy({ ...base, bands: [{ grade: 'a', gte: 5 }, { grade: 'b', gte: 5 }] }),
    /increasing/,
  );
  assert.throws(
    () => normalizePolicy({ ...base, bands: [{ grade: 'a', gte: 0 }, { grade: 'b', gte: null }] }),
    /first band/,
  );
  assert.throws(() => normalizePolicy({ ...base, bands: [] }), /at least one/);
});

test('attribute domains must be finite sets of unique integers', () => {
  assert.throws(
    () => normalizePolicy({ ...base, attributes: { x: [0, 0] } }),
    /duplicate domain value/,
  );
  assert.throws(
    () => normalizePolicy({ ...base, attributes: { x: [] } }),
    /non-empty finite set/,
  );
});
