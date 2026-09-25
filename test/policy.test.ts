import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loadPolicy } from '../src/policy.ts';
import type { PolicySpec } from '../src/policy.ts';

const schema = {
  age: { type: 'int', min: 0, max: 120 },
  tier: { type: 'enum', values: ['free', 'pro', 'enterprise'] },
  verified: { type: 'bool' },
} as const;

const base: PolicySpec = {
  schema,
  thresholds: [
    { grade: 'A', min: 80 },
    { grade: 'B', min: 50 },
    { grade: 'C', min: '-1/2' },
  ],
  rules: [
    { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 10 },
  ],
};

test('identical duplicate rule id is counted once', () => {
  const policy = loadPolicy({
    ...base,
    rules: [
      { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 10 },
      { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: '20/2' },
    ],
  });
  assert.equal(policy.rules.length, 1);
});

test('structurally equal conditions in different order dedupe', () => {
  const c1 = {
    op: 'and',
    args: [
      { op: 'bool', attr: 'verified', value: true },
      { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 },
    ],
  } as const;
  const c2 = {
    op: 'and',
    args: [
      { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 },
      { op: 'bool', attr: 'verified', value: true },
    ],
  } as const;
  const policy = loadPolicy({
    ...base,
    rules: [
      { id: 'r1', when: c1, weight: 5 },
      { id: 'r1', when: c2, weight: 5 },
    ],
  });
  assert.equal(policy.rules.length, 1);
});

test('conflicting duplicate rule id rejects the policy', () => {
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 10 },
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 21 }, weight: 10 },
        ],
      }),
    /conflicting contents for duplicate rule id "r1"/,
  );
  // same condition but different weight is also a conflict
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 10 },
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 11 },
        ],
      }),
    /conflicting contents/,
  );
  // scoring vs hard-reject under the same id is a conflict
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 10 },
          { id: 'r1', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, hardReject: true },
        ],
      }),
    /conflicting contents/,
  );
});

test('thresholds must be explicit, strictly decreasing, unique grades', () => {
  assert.throws(
    () => loadPolicy({ ...base, thresholds: [{ grade: 'A', min: 50 }, { grade: 'B', min: 50 }] }),
    /strictly decreasing/,
  );
  assert.throws(
    () => loadPolicy({ ...base, thresholds: [{ grade: 'A', min: 50 }, { grade: 'A', min: 0 }] }),
    /duplicate grade/,
  );
  assert.throws(() => loadPolicy({ ...base, thresholds: [] }), /at least one/);
});

test('conditions may only use declared attributes and finite constructs', () => {
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [{ id: 'x', when: { op: 'cmp', attr: 'ssn', cmp: 'eq', value: 1 }, weight: 1 }],
      }),
    /undeclared attribute/,
  );
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [{ id: 'x', when: { op: 'cmp', attr: 'tier', cmp: 'eq', value: 1 }, weight: 1 }],
      }),
    /requires int attribute/,
  );
  assert.throws(
    () =>
      loadPolicy({
        ...base,
        rules: [{ id: 'x', when: { op: 'in', attr: 'tier', values: ['gold'] }, weight: 1 }],
      }),
    /outside enum domain/,
  );
});
