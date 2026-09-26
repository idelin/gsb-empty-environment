import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, type Schema } from '../src/index.ts';

test('overlapping groups are checked jointly, not per-group', () => {
  // Each group is individually satisfiable, jointly they are not:
  //   R1 needs b (addable), R2 forbids b once mode='strict' holds.
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
    ],
  };
  const result = validate({ mode: 'strict' }, schema);
  assert.equal(result.status, 'conflict');
  assert.deepEqual(result.completions, []);
  // subset-minimal core over original field conditions: fact + both rules
  assert.deepEqual(result.conflictCores, [
    { facts: ['mode'], rules: ['/groups/0', '/groups/1'] },
  ]);
});

test('the same system is satisfiable when the condition does not fire', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
    ],
  };
  assert.equal(validate({ mode: 'relaxed' }, schema).valid, true);
});

test('core minimality: removing any item makes the remainder satisfiable', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'equals', field: 'mode', value: 'strict' } },
    ],
  };
  // Without the fact (mode absent from the instance) both rules switch off.
  assert.equal(validate({}, schema).valid, true);
  // Without either single rule the remainder is satisfiable.
  for (const drop of [0, 1]) {
    const sub: Schema = { groups: schema.groups.filter((_, i) => i !== drop) };
    assert.equal(validate({ mode: 'strict' }, sub).status !== 'conflict', true);
  }
});

test('overlapping unconditional groups: joint minimum completion, all witnesses', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'atLeastOne', fields: ['b', 'c'] },
    ],
  };
  const result = validate({}, schema);
  assert.equal(result.status, 'missing');
  // size-1 witness {b} satisfies BOTH groups; the joint solver must find it
  assert.deepEqual(result.completions, [['b']]);
});

test('minimum completions enumerate every smallest witness', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'atLeastOne', fields: ['c'] },
    ],
  };
  const result = validate({}, schema);
  assert.equal(result.status, 'missing');
  assert.deepEqual(result.completions, [['a', 'c'], ['b', 'c']]);
});

test('completion may work by DISABLING a conditional group (non-monotone conditions)', () => {
  // R1 needs b; R2 forbids b while c is absent. Adding c disables R2.
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['b'] },
      { kind: 'atMostK', k: 0, fields: ['b'], when: { op: 'absent', field: 'c' } },
    ],
  };
  const result = validate({}, schema);
  assert.equal(result.status, 'missing');
  assert.deepEqual(result.completions, [['b', 'c']], 'only the joint completion {b,c} works');
});

test('excess presence across overlapping groups yields a minimal core', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'exactlyOne', fields: ['b', 'c'] },
    ],
  };
  const result = validate({ a: 1, b: 1 }, schema);
  assert.equal(result.status, 'conflict');
  assert.deepEqual(result.conflictCores, [{ facts: ['a', 'b'], rules: ['/groups/0'] }]);
});

test('irrelevant rules and facts stay out of the minimal core', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'atLeastOne', fields: ['unrelated'] },
    ],
  };
  const result = validate({ a: 1, b: 1, unrelated: true }, schema);
  assert.equal(result.status, 'conflict');
  assert.deepEqual(result.conflictCores, [{ facts: ['a', 'b'], rules: ['/groups/0'] }]);
});
