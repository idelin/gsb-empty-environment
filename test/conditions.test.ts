import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evalCondition, type View } from '../src/conditions.ts';

const viewOf = (obj: Record<string, unknown>): View => ({
  has: (f) => Object.prototype.hasOwnProperty.call(obj, f),
  valueKnown: (f) => Object.prototype.hasOwnProperty.call(obj, f),
  get: (f) => obj[f],
});

test('present/absent are always decidable', () => {
  const view = viewOf({ a: 1 });
  assert.equal(evalCondition({ op: 'present', field: 'a' }, view), 'true');
  assert.equal(evalCondition({ op: 'present', field: 'z' }, view), 'false');
  assert.equal(evalCondition({ op: 'absent', field: 'z' }, view), 'true');
  assert.equal(evalCondition({ op: 'absent', field: 'a' }, view), 'false');
});

test('equals on a missing field is undecided, not false', () => {
  const view = viewOf({});
  assert.equal(evalCondition({ op: 'equals', field: 'x', value: 1 }, view), 'undecided');
  assert.equal(evalCondition({ op: 'notEquals', field: 'x', value: 1 }, view), 'undecided');
});

test('equals compares deeply', () => {
  const view = viewOf({ x: { p: [1, 2] } });
  assert.equal(evalCondition({ op: 'equals', field: 'x', value: { p: [1, 2] } }, view), 'true');
  assert.equal(evalCondition({ op: 'equals', field: 'x', value: { p: [1, 3] } }, view), 'false');
});

test('and/or/not propagate undecided (Kleene logic)', () => {
  const view = viewOf({ a: 1 });
  const und = { op: 'equals', field: 'gone', value: 1 } as const;
  assert.equal(
    evalCondition({ op: 'and', all: [{ op: 'present', field: 'a' }, und] }, view),
    'undecided',
  );
  assert.equal(
    evalCondition({ op: 'and', all: [{ op: 'absent', field: 'a' }, und] }, view),
    'false',
  );
  assert.equal(
    evalCondition({ op: 'or', any: [{ op: 'present', field: 'a' }, und] }, view),
    'true',
  );
  assert.equal(
    evalCondition({ op: 'or', any: [{ op: 'absent', field: 'a' }, und] }, view),
    'undecided',
  );
  assert.equal(evalCondition({ op: 'not', cond: und }, view), 'undecided');
});

test('a field present only in a hypothetical completion has unknown value', () => {
  const view: View = {
    has: (f) => f === 'added',
    valueKnown: () => false,
    get: () => undefined,
  };
  assert.equal(evalCondition({ op: 'present', field: 'added' }, view), 'true');
  assert.equal(evalCondition({ op: 'equals', field: 'added', value: 1 }, view), 'undecided');
});
