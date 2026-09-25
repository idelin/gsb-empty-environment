import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GradingSession } from '../src/session.ts';
import { evaluate } from '../src/engine.ts';
import { normalizePolicy, type PolicySpec } from '../src/policy.ts';

const spec: PolicySpec = {
  attributes: { x: [0, 1], y: [0, 1] },
  rules: [
    { id: 'wx', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: '1/3' },
    { id: 'wy', when: { kind: 'cmp', attr: 'y', op: 'eq', value: 1 }, weight: '2/3' },
    {
      id: 'both',
      when: { kind: 'and', args: [
        { kind: 'cmp', attr: 'x', op: 'eq', value: 1 },
        { kind: 'cmp', attr: 'y', op: 'eq', value: 1 },
      ] },
      hardReject: true,
    },
  ],
  bands: [
    { grade: 'low', gte: null },
    { grade: 'high', gte: '1/2' },
  ],
};

function fresh(known: Record<string, number>) {
  return evaluate(normalizePolicy(spec), known);
}

test('incremental recompute after attribute correction matches fresh evaluation', () => {
  const s = new GradingSession(spec, { x: 0 });

  let r = s.evaluate();
  assert.equal(r.status, 'pending'); // y unknown: low (y=0) or high (y=1)

  s.setAttribute('y', 1); // correction arrives
  r = s.evaluate();
  assert.equal(r.status, 'definite');
  assert.deepEqual(r.definiteOutcome, fresh({ x: 0, y: 1 }).definiteOutcome);

  s.setAttribute('x', 1); // correction: x was actually 1 -> hard reject fires
  r = s.evaluate();
  assert.equal(r.status, 'definite');
  assert.deepEqual(r.definiteOutcome, { type: 'reject', ruleIds: ['both'] });

  s.setAttribute('x', 0); // corrected back
  r = s.evaluate();
  assert.deepEqual(r.definiteOutcome, fresh({ x: 0, y: 1 }).definiteOutcome);

  s.removeAttribute('y'); // y becomes unknown again
  r = s.evaluate();
  assert.equal(r.status, 'pending');
  assert.deepEqual(
    r.possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : 'REJECT')),
    fresh({ x: 0 }).possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : 'REJECT')),
  );
});

test('unchanged inputs reuse the enumeration; unrelated corrections do not re-enumerate', () => {
  const s = new GradingSession(spec, { x: 0 });
  s.evaluate();
  const afterFirst = { ...s.stats };
  assert.ok(afterFirst.assignmentsEnumerated > 0);

  s.evaluate(); // nothing changed
  assert.equal(s.stats.enumerationReuses, 1);
  assert.equal(s.stats.assignmentsEnumerated, afterFirst.assignmentsEnumerated);
  assert.equal(s.stats.enumRuleEvals, afterFirst.enumRuleEvals);

  s.setAttribute('x', 0); // same value: not dirty
  s.evaluate();
  assert.equal(s.stats.enumerationReuses, 2);

  s.setAttribute('x', 1); // real correction: enumeration must rerun
  s.evaluate();
  assert.ok(s.stats.assignmentsEnumerated > afterFirst.assignmentsEnumerated);
});

test('static rules are re-evaluated only when their own dependencies change', () => {
  const wideSpec: PolicySpec = {
    attributes: { x: [0, 1], y: [0, 1], z: [0, 1] },
    rules: [
      { id: 'rx', when: { kind: 'cmp', attr: 'x', op: 'eq', value: 1 }, weight: 1 },
      { id: 'rz', when: { kind: 'cmp', attr: 'z', op: 'eq', value: 1 }, weight: 1 },
      { id: 'dyn', when: { kind: 'cmp', attr: 'y', op: 'eq', value: 1 }, weight: 1 },
    ],
    bands: [{ grade: 'g', gte: null }],
  };
  const s = new GradingSession(wideSpec, { x: 1, z: 1 }); // y unknown
  s.evaluate();
  const evalsAfterFirst = s.stats.staticRuleEvals;
  assert.equal(evalsAfterFirst, 2); // rx + rz evaluated once each

  s.setAttribute('x', 0); // only rx depends on x
  s.evaluate();
  assert.equal(s.stats.staticRuleEvals, evalsAfterFirst + 1);
});

test('session accepts a pre-normalized policy', () => {
  const policy = normalizePolicy(spec);
  const s = new GradingSession(policy, { x: 1, y: 0 });
  const r = s.evaluate();
  assert.equal(r.status, 'definite');
  assert.equal((r.definiteOutcome as any).grade, 'low');
});
