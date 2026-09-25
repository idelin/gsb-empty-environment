import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluatePolicy, type Policy } from '../src/policy.ts';

const policy: Policy = {
  name: 'transfer-policy',
  requiredAttributes: ['role', 'amount', 'mfaPassed'],
  condition: {
    op: 'and',
    all: [
      { op: 'in', attr: 'role', set: ['admin', 'operator'] },
      { op: 'lte', attr: 'amount', value: 10000 },
      {
        op: 'or',
        any: [
          { op: 'eq', attr: 'amount', value: 0 },
          { op: 'not', cond: { op: 'in', attr: 'role', set: ['guest'] } },
        ],
      },
    ],
  },
};

test('allows when finite-set, integer and boolean conditions all hold', () => {
  const decision = evaluatePolicy(policy, { role: 'admin', amount: 5000, mfaPassed: true });
  assert.deepEqual(decision, { decision: 'allow' });
});

test('denies when finite-set membership fails', () => {
  const decision = evaluatePolicy(policy, { role: 'guest', amount: 5, mfaPassed: true });
  assert.equal(decision.decision, 'deny');
});

test('denies when integer bound is exceeded', () => {
  const decision = evaluatePolicy(policy, { role: 'admin', amount: 10001, mfaPassed: true });
  assert.equal(decision.decision, 'deny');
});

test('denies when a required attribute is missing', () => {
  const decision = evaluatePolicy(policy, { role: 'admin', mfaPassed: true } as never);
  assert.equal(decision.decision, 'deny');
  assert.match((decision as { reasons: string[] }).reasons.join(','), /missing attribute "amount"/);
});

test('denies non-integer attribute under integer condition', () => {
  const decision = evaluatePolicy(policy, { role: 'admin', amount: 1.5, mfaPassed: true });
  assert.equal(decision.decision, 'deny');
  assert.match((decision as { reasons: string[] }).reasons.join(','), /integer/);
});

test('denies non-integer comparison value in the condition itself', () => {
  const bad: Policy = {
    name: 'bad',
    requiredAttributes: ['amount'],
    condition: { op: 'lt', attr: 'amount', value: 0.5 },
  };
  const decision = evaluatePolicy(bad, { amount: 0 });
  assert.equal(decision.decision, 'deny');
});

test('denies conditions that read attributes outside the explicit list', () => {
  const sneaky: Policy = {
    name: 'sneaky',
    requiredAttributes: ['role'],
    condition: { op: 'eq', attr: 'secretFromIdp', value: 1 },
  };
  const decision = evaluatePolicy(sneaky, { role: 'admin', secretFromIdp: 1 } as never);
  assert.equal(decision.decision, 'deny');
  assert.match((decision as { reasons: string[] }).reasons.join(','), /explicit attribute list/);
});
