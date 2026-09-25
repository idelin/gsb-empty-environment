import test from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEvaluator } from '../src/evaluator.ts';
import { and, cmp } from '../src/policy.ts';
import { ScriptedSource, valuesOutcome } from './helpers.ts';

test('deny decided by a cheap false condition never fetches the sensitive expensive attribute', async () => {
  const profile = new ScriptedSource('profile', new Set(['age']), 1, 0, () =>
    valuesOutcome('v1', { age: 10 }),
  );
  const sensitive = new ScriptedSource('sensitive', new Set(['ssn']), 1000, 0, () =>
    valuesOutcome('v1', { ssn: 12345 }),
  );
  const ev = new PolicyEvaluator([profile, sensitive]);
  const policy = and(cmp('age', 'ge', 18), cmp('ssn', 'eq', 12345));

  const plan = ev.planOf(policy);
  assert.deepEqual([...plan.mustRead], ['age']);
  assert.deepEqual([...plan.optional], ['ssn']);
  assert.equal(plan.claimsOptimal, false);

  const rec = await ev.evaluate(policy, { subject: { id: 'u1' }, permission: 'guest' });
  assert.equal(rec.decision, 'deny');
  assert.equal(sensitive.calls.length, 0, 'sensitive source must not be called');
  assert.equal(profile.calls.length, 1);
  assert.deepEqual(
    rec.evidence.map((e) => e.key),
    ['age'],
  );
});

test('policy validation rejects undeclared attributes and non-integer conditions', async () => {
  const src = new ScriptedSource('s', new Set(['age']), 1, 0, () => valuesOutcome('v1', { age: 1 }));
  const ev = new PolicyEvaluator([src]);
  assert.throws(() => ev.planOf(cmp('unknown', 'eq', 1)), /undeclared attribute/);
  assert.throws(() => ev.planOf(cmp('age', 'eq', 1.5)), /integer/);
});
