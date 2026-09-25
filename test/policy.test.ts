import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, PolicyError, type Policy } from '../src/policy.ts';

const policy: Policy = {
  id: 'purchase',
  condition: {
    op: 'and',
    all: [
      { op: 'in', attr: 'role', set: ['buyer', 'vip'] },
      { op: 'gte', attr: 'balance', value: 100 },
      { op: 'lt', attr: 'strikes', value: 3 },
      {
        op: 'or',
        any: [
          { op: 'eq', attr: 'level', value: 2 },
          { op: 'not', cond: { op: 'in', attr: 'region', set: ['embargoed'] } },
        ],
      },
    ],
  },
};

test('allows when finite-set, integer and boolean conditions all hold', () => {
  assert.equal(
    evaluate(policy, { role: 'vip', balance: 150, strikes: 0, level: 2, region: 'embargoed' }),
    true,
  );
  assert.equal(
    evaluate(policy, { role: 'buyer', balance: 100, strikes: 2, level: 1, region: 'eu' }),
    true,
  );
});

test('denies when any condition fails', () => {
  const base = { role: 'buyer', balance: 100, strikes: 0, level: 2, region: 'eu' };
  assert.equal(evaluate(policy, { ...base, role: 'guest' }), false);
  assert.equal(evaluate(policy, { ...base, balance: 99 }), false);
  assert.equal(evaluate(policy, { ...base, strikes: 3 }), false);
  assert.equal(evaluate(policy, { ...base, level: 1, region: 'embargoed' }), false);
});

test('denies when a required attribute is missing or non-integer', () => {
  assert.equal(evaluate(policy, { balance: 100, strikes: 0, level: 2 }), false);
  const bal = { role: 'buyer', strikes: 0, level: 2, region: 'eu' };
  assert.equal(evaluate(policy, { ...bal, balance: 100.5 }), false);
  assert.equal(evaluate(policy, { ...bal, balance: '100' }), false);
});

test('rejects non-integer literals in integer conditions', () => {
  const bad: Policy = {
    id: 'bad',
    condition: { op: 'gte', attr: 'balance', value: 99.5 },
  };
  assert.throws(() => evaluate(bad, { balance: 100 }), PolicyError);
});

test('reads only the explicitly named attributes', () => {
  const simple: Policy = {
    id: 'simple',
    condition: { op: 'in', attr: 'tier', set: ['a', 'b'] },
  };
  assert.equal(evaluate(simple, { tier: 'a', anythingElse: 'ignored' }), true);
  assert.equal(evaluate(simple, { anythingElse: 'x' }), false);
});
