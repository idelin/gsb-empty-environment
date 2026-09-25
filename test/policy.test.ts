import test from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, validateCondition, PolicyError } from '../src/policy.ts';
import type { Condition } from '../src/types.ts';

test('in: finite-set membership for strings and integers', () => {
  const cond: Condition = { op: 'in', attr: 'role', set: ['analyst', 'auditor'] };
  assert.equal(evaluate(cond, { role: 'analyst' }).ok, true);
  assert.equal(evaluate(cond, { role: 'admin' }).ok, false);

  const levels: Condition = { op: 'in', attr: 'level', set: [1, 2, 3] };
  assert.equal(evaluate(levels, { level: 2 }).ok, true);
  assert.equal(evaluate(levels, { level: 9 }).ok, false);
});

test('integer comparisons: all operators', () => {
  const attrs = { clearance: 3 };
  assert.equal(evaluate({ op: 'eq', attr: 'clearance', value: 3 }, attrs).ok, true);
  assert.equal(evaluate({ op: 'lt', attr: 'clearance', value: 5 }, attrs).ok, true);
  assert.equal(evaluate({ op: 'lte', attr: 'clearance', value: 3 }, attrs).ok, true);
  assert.equal(evaluate({ op: 'gt', attr: 'clearance', value: 2 }, attrs).ok, true);
  assert.equal(evaluate({ op: 'gte', attr: 'clearance', value: 4 }, attrs).ok, false);
});

test('non-integer or missing attributes make atomic conditions false', () => {
  const cond: Condition = { op: 'gte', attr: 'clearance', value: 2 };
  assert.equal(evaluate(cond, { clearance: 2.5 }).ok, false);
  assert.equal(evaluate(cond, { clearance: 'high' }).ok, false);
  assert.equal(evaluate(cond, {}).ok, false);
  assert.equal(evaluate({ op: 'in', attr: 'role', set: ['a'] }, {}).ok, false);
});

test('boolean and/or/not compose', () => {
  const cond: Condition = {
    op: 'and',
    conditions: [
      { op: 'in', attr: 'dept', set: ['eng', 'ops'] },
      { op: 'gte', attr: 'clearance', value: 2 },
      { op: 'not', condition: { op: 'in', attr: 'status', set: ['suspended'] } },
    ],
  };
  assert.equal(evaluate(cond, { dept: 'eng', clearance: 3, status: 'active' }).ok, true);
  assert.equal(evaluate(cond, { dept: 'eng', clearance: 1, status: 'active' }).ok, false);
  assert.equal(evaluate(cond, { dept: 'eng', clearance: 3, status: 'suspended' }).ok, false);

  const either: Condition = {
    op: 'or',
    conditions: [
      { op: 'eq', attr: 'clearance', value: 5 },
      { op: 'in', attr: 'role', set: ['owner'] },
    ],
  };
  assert.equal(evaluate(either, { clearance: 1, role: 'owner' }).ok, true);
  assert.equal(evaluate(either, { clearance: 1, role: 'guest' }).ok, false);
});

test('evaluation reports exactly which explicit attributes were read', () => {
  const cond: Condition = {
    op: 'and',
    conditions: [
      { op: 'gte', attr: 'clearance', value: 2 },
      { op: 'in', attr: 'dept', set: ['eng'] },
      { op: 'gte', attr: 'clearance', value: 1 },
    ],
  };
  const outcome = evaluate(cond, { clearance: 3, dept: 'eng', ignored: 'x' });
  assert.equal(outcome.ok, true);
  assert.deepEqual(outcome.attributesRead, ['clearance', 'dept']);
});

test('validation rejects non-integer values, empty sets, bad members', () => {
  assert.throws(() => validateCondition({ op: 'gte', attr: 'x', value: 1.5 }), PolicyError);
  assert.throws(() => validateCondition({ op: 'in', attr: 'x', set: [] }), PolicyError);
  assert.throws(() => validateCondition({ op: 'in', attr: 'x', set: [1.5] }), PolicyError);
  assert.throws(() => validateCondition({ op: 'and', conditions: [] }), PolicyError);
  assert.throws(
    () => validateCondition({ op: 'not', condition: { op: 'eq', attr: 'x', value: Number.NaN } }),
    PolicyError,
  );
});
