import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, EvaluationError, REJECT } from '../src/engine.ts';
import { normalizePolicy, type PolicySpec } from '../src/policy.ts';
import { Rational } from '../src/rational.ts';

function run(spec: PolicySpec, known: Record<string, number> = {}) {
  return evaluate(normalizePolicy(spec), known);
}

const twoBands = [
  { grade: 'low', gte: null },
  { grade: 'high', gte: 10 },
] as const;

test('hard reject is not offset by any score', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [
      { id: 'bonus', when: { kind: 'const', value: true }, weight: 1000 },
      { id: 'blacklist', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, hardReject: true },
    ],
    bands: [...twoBands],
  };
  const rejected = run(spec, { x: 1 });
  assert.equal(rejected.status, 'definite');
  assert.deepEqual(rejected.definiteOutcome, { type: 'reject', ruleIds: ['blacklist'] });
  assert.equal(rejected.scoreBounds, null);

  const clean = run(spec, { x: 0 });
  assert.equal(clean.status, 'definite');
  assert.deepEqual(clean.definiteOutcome, {
    type: 'grade',
    grade: 'high',
    score: Rational.of(1000),
  });
});

test('negative weights pull the score below zero', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [
      { id: 'base', when: { kind: 'const', value: true }, weight: 4 },
      { id: 'penalty', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: '-7/2' },
    ],
    bands: [
      { grade: 'bad', gte: null },
      { grade: 'ok', gte: 0 },
      { grade: 'good', gte: 3 },
    ],
  };
  const r = run(spec, { x: 1 });
  assert.equal(r.status, 'definite');
  assert.deepEqual(r.definiteOutcome, { type: 'grade', grade: 'ok', score: Rational.of(1, 2) });
});

test('shared unknown keeps terms correlated: mutually exclusive rules give exact point bounds', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [
      { id: 'r0', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 0 }, weight: 5 },
      { id: 'r1', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: 5 },
    ],
    bands: [...twoBands],
  };
  const r = run(spec); // x missing
  // Independent per-term extrema would claim [0, 10]; the joint truth is [5, 5].
  assert.equal(r.scoreBounds!.min.toString(), '5');
  assert.equal(r.scoreBounds!.max.toString(), '5');
  assert.equal(r.status, 'definite');
  assert.deepEqual(r.definiteOutcome, { type: 'grade', grade: 'low', score: Rational.of(5) });
});

test('mutually exclusive "in" conditions never fire together', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1, 2] },
    rules: [
      { id: 'a', when: { kind: 'in', attr: 'x', set: [0] }, weight: 7 },
      { id: 'b', when: { kind: 'in', attr: 'x', set: [1] }, weight: 7 },
    ],
    bands: [...twoBands],
  };
  const r = run(spec);
  // x=2 gives 7 as well; bounds are [0, 7], never 14.
  assert.equal(r.scoreBounds!.min.toString(), '0');
  assert.equal(r.scoreBounds!.max.toString(), '7');
  assert.equal(r.status, 'definite'); // all assignments grade "low"
  assert.equal((r.definiteOutcome as any).grade, 'low');
  assert.equal((r.definiteOutcome as any).score, null); // score varies: 0 or 7
});

test('interval straddling a threshold is pending, never decided by midpoint', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [{ id: 'big', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: 12 }],
    bands: [...twoBands],
  };
  const r = run(spec); // scores {0, 12}; midpoint 6 would wrongly say "low"
  assert.equal(r.status, 'pending');
  assert.deepEqual(
    r.possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : 'REJECT')),
    ['low', 'high'],
  );
  assert.equal(r.scoreBounds!.min.toString(), '0');
  assert.equal(r.scoreBounds!.max.toString(), '12');
  // witness per reachable grade
  assert.equal(r.witnesses.get('low')!.x, 0);
  assert.equal(r.witnesses.get('high')!.x, 1);
});

test('uncertain hard reject yields pending with REJECT among outcomes', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [
      { id: 'bonus', when: { kind: 'const', value: true }, weight: 20 },
      { id: 'fraud', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, hardReject: true },
    ],
    bands: [...twoBands],
  };
  const r = run(spec);
  assert.equal(r.status, 'pending');
  assert.deepEqual(
    r.possibleOutcomes.map((o) => o.type),
    ['grade', 'reject'],
  );
  assert.deepEqual(r.rejectRuleIds, ['fraud']);
  assert.ok(r.witnesses.has(REJECT));
  assert.equal(r.witnesses.get(REJECT)!.x, 1);
  assert.equal(r.witnesses.get('high')!.x, 0);
});

test('unconditional hard reject is definite regardless of weights', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [
      { id: 'deny', when: { kind: 'const', value: true }, hardReject: true },
      { id: 'bonus', when: { kind: 'const', value: true }, weight: 50 },
    ],
    bands: [...twoBands],
  };
  const r = run(spec);
  assert.equal(r.status, 'definite');
  assert.equal(r.definiteOutcome!.type, 'reject');
  assert.equal(r.scoreBounds, null);
});

test('boolean combinators and rational threshold boundaries', () => {
  const spec: PolicySpec = {
    attributes: { a: [0, 1], b: [0, 1] },
    rules: [
      {
        id: 'combo',
        when: {
          kind: 'or',
          args: [
            { kind: 'and', args: [
              { kind: 'cmp', attr: 'a', op: 'ge', value: 1 },
              { kind: 'not', arg: { kind: 'cmp', attr: 'b', op: 'eq', value: 1 } },
            ] },
            { kind: 'cmp', attr: 'b', op: 'gt', value: 0 },
          ],
        },
        weight: '1/3',
      },
    ],
    bands: [
      { grade: 'no', gte: null },
      { grade: 'yes', gte: '1/3' },
    ],
  };
  // combo fires for (a,b) in {(1,0),(0,1),(1,1)} -> score exactly 1/3 -> "yes"
  const yes = run(spec, { a: 1, b: 0 });
  assert.deepEqual(yes.definiteOutcome, { type: 'grade', grade: 'yes', score: Rational.of(1, 3) });
  const no = run(spec, { a: 0, b: 0 });
  assert.deepEqual(no.definiteOutcome, { type: 'grade', grade: 'no', score: Rational.of(0) });
});

test('input values must be integers inside the declared finite domain', () => {
  const spec: PolicySpec = {
    attributes: { x: [0, 1] },
    rules: [],
    bands: [{ grade: 'only', gte: null }],
  };
  assert.throws(() => run(spec, { x: 2 }), EvaluationError);
  assert.throws(() => run(spec, { y: 0 }), /not declared/);
});

test('assignment space cap is enforced', () => {
  const spec: PolicySpec = {
    attributes: { a: [0, 1, 2], b: [0, 1, 2], c: [0, 1, 2] },
    rules: [],
    bands: [{ grade: 'only', gte: null }],
  };
  assert.throws(() => evaluate(normalizePolicy(spec), {}, { maxAssignments: 10 }), /too large/);
  const ok = evaluate(normalizePolicy(spec), {}, { maxAssignments: 27 });
  assert.equal(ok.assignmentsExplored, 27);
});
