import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Policy, Rule } from '../src/types.ts';
import { checkConflicts } from '../src/policy.ts';

const policy = (rules: Rule[]): Policy => ({ name: 'p', defaultAction: 'exclude', rules });
const kinds = (rules: Rule[]) => checkConflicts(policy(rules)).map((c) => c.kind);

test('same priority, overlapping conditions, different actions is ambiguous', () => {
  const conflicts = checkConflicts(policy([
    { id: 'a', priority: 5, action: 'include', when: { kind: 'size', min: 0, max: 100 } },
    { id: 'b', priority: 5, action: 'exclude', when: { kind: 'size', min: 50, max: 200 } },
  ]));
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].kind, 'ambiguous');
  assert.deepEqual(conflicts[0].ruleIds, ['a', 'b']);
});

test('disjoint intervals never conflict', () => {
  assert.deepEqual(kinds([
    { id: 'a', priority: 5, action: 'include', when: { kind: 'size', min: 0, max: 10 } },
    { id: 'b', priority: 5, action: 'exclude', when: { kind: 'size', min: 11, max: 20 } },
  ]), []);
});

test('boundary-touching intervals (max == min) do overlap', () => {
  assert.deepEqual(kinds([
    { id: 'a', priority: 5, action: 'include', when: { kind: 'size', min: 0, max: 10 } },
    { id: 'b', priority: 5, action: 'exclude', when: { kind: 'size', min: 10, max: 20 } },
  ]), ['ambiguous']);
});

test('a rule fully covered by a higher-priority rule is shadowed or redundant', () => {
  const shadowed = checkConflicts(policy([
    { id: 'hi', priority: 9, action: 'exclude', when: { kind: 'path', glob: 'tmp/**' } },
    { id: 'lo', priority: 1, action: 'include', when: { kind: 'path', glob: 'tmp/cache/**' } },
  ]));
  assert.deepEqual(shadowed.map((c) => c.kind), ['shadowed']);

  const redundant = checkConflicts(policy([
    { id: 'hi', priority: 9, action: 'exclude', when: { kind: 'path', glob: 'tmp/**' } },
    { id: 'lo', priority: 1, action: 'exclude', when: { kind: 'path', glob: 'tmp/cache/**' } },
  ]));
  assert.deepEqual(redundant.map((c) => c.kind), ['redundant']);
});

test('unsatisfiable rule (min > max) is reported', () => {
  const conflicts = checkConflicts(policy([
    { id: 'dead', priority: 1, action: 'include', when: { kind: 'size', min: 10, max: 5 } },
  ]));
  assert.deepEqual(conflicts.map((c) => c.kind), ['unsatisfiable-rule']);
});

test('required vs forbidden tag makes a rule unsatisfiable', () => {
  const conflicts = checkConflicts(policy([
    {
      id: 'dead', priority: 1, action: 'include',
      when: { kind: 'all', conds: [
        { kind: 'tag', tag: 'x' },
        { kind: 'tag', tag: 'x', negate: true },
      ] },
    },
  ]));
  assert.deepEqual(conflicts.map((c) => c.kind), ['unsatisfiable-rule']);
});

test('partial overlap across priorities with different actions is an overlap note', () => {
  const conflicts = checkConflicts(policy([
    { id: 'hi', priority: 9, action: 'exclude', when: { kind: 'size', min: 0, max: 100 } },
    { id: 'lo', priority: 1, action: 'include', when: { kind: 'size', min: 50, max: 500 } },
  ]));
  assert.deepEqual(conflicts.map((c) => c.kind), ['overlap']);
});

test('literal path globs are disjoint when different', () => {
  assert.deepEqual(kinds([
    { id: 'a', priority: 5, action: 'include', when: { kind: 'path', glob: 'a.txt' } },
    { id: 'b', priority: 5, action: 'exclude', when: { kind: 'path', glob: 'b.txt' } },
  ]), []);
});
