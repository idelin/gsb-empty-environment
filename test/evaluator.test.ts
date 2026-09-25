import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loadPolicy } from '../src/policy.ts';
import { evaluate, naiveIndependentBounds } from '../src/evaluator.ts';
import { toString } from '../src/rational.ts';

const schema = {
  age: { type: 'int', min: 0, max: 120 },
  tier: { type: 'enum', values: ['free', 'pro', 'enterprise'] },
  verified: { type: 'bool' },
  strikes: { type: 'int', min: 0, max: 3 },
} as const;

const thresholds = [
  { grade: 'A', min: 80 },
  { grade: 'B', min: 50 },
  { grade: 'C', min: 0 },
] as const;

test('fully known attributes give a definite grade', () => {
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 40 },
      { id: 'pro', when: { op: 'in', attr: 'tier', values: ['pro', 'enterprise'] }, weight: 30 },
      { id: 'verified', when: { op: 'bool', attr: 'verified', value: true }, weight: 15 },
    ],
  });
  const r = evaluate(policy, { age: 30, tier: 'pro', verified: true, strikes: 0 });
  assert.equal(r.status, 'definite');
  assert.equal(r.outcome?.kind, 'grade');
  if (r.outcome?.kind === 'grade') {
    assert.equal(r.outcome.grade, 'A');
    assert.equal(toString(r.outcome.score), '85');
  }
  assert.equal(r.assignmentsExplored, 1);
});

test('hard reject is never offset by a high score', () => {
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'big-bonus', when: { op: 'bool', attr: 'verified', value: true }, weight: 500 },
      { id: 'banned', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 2 }, hardReject: true },
    ],
  });
  const r = evaluate(policy, { age: 30, tier: 'enterprise', verified: true, strikes: 3 });
  assert.equal(r.status, 'definite');
  assert.equal(r.outcome?.kind, 'rejected');
});

test('negative weights pull the score down across a threshold', () => {
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 60 },
      { id: 'penalty', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 1 }, weight: '-25/2' },
    ],
  });
  const withPenalty = evaluate(policy, { age: 40, tier: 'free', verified: false, strikes: 1 });
  assert.equal(withPenalty.status, 'definite');
  if (withPenalty.outcome?.kind === 'grade') {
    assert.equal(toString(withPenalty.outcome.score), '95/2'); // 60 - 12.5 = 47.5 -> C? no: >= 0, < 50
    assert.equal(withPenalty.outcome.grade, 'C');
  }
  const clean = evaluate(policy, { age: 40, tier: 'free', verified: false, strikes: 0 });
  if (clean.outcome?.kind === 'grade') assert.equal(clean.outcome.grade, 'B');
});

test('missing attribute yields undecided result with per-grade witnesses', () => {
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 40 },
      { id: 'pro', when: { op: 'in', attr: 'tier', values: ['pro', 'enterprise'] }, weight: 45 },
    ],
  });
  // tier unknown: free -> 40 (C), pro/enterprise -> 85 (A)
  const r = evaluate(policy, { age: 30, verified: true, strikes: 0 });
  assert.equal(r.status, 'undecided');
  const grades = r.outcomes.map((o) => (o.kind === 'grade' ? o.grade : 'rejected')).sort();
  assert.deepEqual(grades, ['A', 'C']);
  for (const o of r.outcomes) {
    assert.ok(o.witness.tier !== undefined, 'every outcome carries a witness assignment');
  }
  assert.deepEqual(r.unknownVariables, ['tier']);
  assert.equal(r.assignmentsExplored, 3);
});

test('interval straddling a threshold is not decided by midpoint', () => {
  // scores reachable: 45 (C) or 85 (A); midpoint of bounds is 65 which would
  // wrongly suggest a definite B
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'base', when: { op: 'const', value: true }, weight: 45 },
      { id: 'pro', when: { op: 'in', attr: 'tier', values: ['pro', 'enterprise'] }, weight: 40 },
    ],
  });
  const r = evaluate(policy, { age: 10, verified: false, strikes: 0 });
  assert.equal(r.status, 'undecided');
  assert.equal(toString(r.scoreBounds.min), '45');
  assert.equal(toString(r.scoreBounds.max), '85');
  const grades = r.outcomes.map((o) => (o.kind === 'grade' ? o.grade : null)).sort();
  assert.deepEqual(grades, ['A', 'C']);
  assert.ok(!grades.includes('B'), 'midpoint grade B is not actually reachable');
});

test('shared unknown variable keeps bounds correlated (exact, not per-term)', () => {
  // rule1: +10 if x >= 1 ; rule2: -10 if x >= 1. For every value of x the
  // total is exactly 0. Independent per-term extremes would claim [-10, 10].
  const correlatedSchema = { x: { type: 'int', min: 0, max: 1 } } as const;
  const policy = loadPolicy({
    schema: correlatedSchema,
    thresholds: [
      { grade: 'hi', min: 5 },
      { grade: 'mid', min: -5 },
      { grade: 'lo', min: -100 },
    ],
    rules: [
      { id: 'up', when: { op: 'cmp', attr: 'x', cmp: 'gte', value: 1 }, weight: 10 },
      { id: 'down', when: { op: 'cmp', attr: 'x', cmp: 'gte', value: 1 }, weight: -10 },
    ],
  });
  const r = evaluate(policy, {});
  assert.equal(r.status, 'definite');
  if (r.outcome?.kind === 'grade') assert.equal(r.outcome.grade, 'mid');
  assert.equal(toString(r.scoreBounds.min), '0');
  assert.equal(toString(r.scoreBounds.max), '0');
  assert.deepEqual(r.reachableScores.map(toString), ['0']);

  const naive = naiveIndependentBounds(policy, {});
  assert.equal(toString(naive.min), '-10');
  assert.equal(toString(naive.max), '10');
  // naive bounds straddle all three thresholds -> would falsely report undecided
});

test('mutually exclusive conditions never fire together', () => {
  // x >= 5 and x < 5 cannot both hold; the reachable score set must reflect
  // exactly one of the two branches for every value of x
  const mutexSchema = { x: { type: 'int', min: 0, max: 9 } } as const;
  const policy = loadPolicy({
    schema: mutexSchema,
    thresholds: [
      { grade: 'hi', min: 7 },
      { grade: 'lo', min: 0 },
    ],
    rules: [
      { id: 'high-branch', when: { op: 'cmp', attr: 'x', cmp: 'gte', value: 5 }, weight: 8 },
      { id: 'low-branch', when: { op: 'cmp', attr: 'x', cmp: 'lt', value: 5 }, weight: 2 },
    ],
  });
  const r = evaluate(policy, {});
  assert.equal(r.status, 'undecided');
  assert.deepEqual(r.reachableScores.map(toString), ['2', '8']);
  const grades = r.outcomes.map((o) => (o.kind === 'grade' ? o.grade : null)).sort();
  assert.deepEqual(grades, ['hi', 'lo']);
  // witness for 'hi' must satisfy x >= 5, witness for 'lo' must satisfy x < 5
  for (const o of r.outcomes) {
    if (o.kind === 'grade' && o.grade === 'hi') assert.ok((o.witness.x as number) >= 5);
    if (o.kind === 'grade' && o.grade === 'lo') assert.ok((o.witness.x as number) < 5);
  }
});

test('undecided between grade and rejection when reject rule depends on unknown', () => {
  const policy = loadPolicy({
    schema,
    thresholds,
    rules: [
      { id: 'adult', when: { op: 'cmp', attr: 'age', cmp: 'gte', value: 18 }, weight: 90 },
      { id: 'banned', when: { op: 'cmp', attr: 'strikes', cmp: 'gte', value: 2 }, hardReject: true },
    ],
  });
  // strikes unknown in 0..3: 0/1 -> grade A, 2/3 -> rejected
  const r = evaluate(policy, { age: 25, tier: 'free', verified: false });
  assert.equal(r.status, 'undecided');
  assert.equal(r.hardRejectPossible, true);
  const kinds = r.outcomes.map((o) => (o.kind === 'rejected' ? 'rejected' : o.grade)).sort();
  assert.deepEqual(kinds, ['A', 'rejected']);
});
