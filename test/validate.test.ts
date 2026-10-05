import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateScript } from '../src/validate.ts';

const base = {
  nodes: ['A', 'B'],
  steps: [
    { id: 's1', type: 'delay', ms: 3 },
    { id: 's2', type: 'restart', node: 'A', dependsOn: ['s1'] },
    { id: 's3', type: 'drop', from: 'A', to: 'B', count: 1, dependsOn: ['s2'] },
  ],
};

test('accepts a well-formed script', () => {
  assert.deepEqual(validateScript(base), []);
});

test('rejects duplicate step ids', () => {
  const script = { ...base, steps: [base.steps[0], base.steps[0]] };
  assert.ok(validateScript(script).some((e) => e.includes('duplicate step id')));
});

test('rejects dependsOn pointing at a later step', () => {
  const script = {
    ...base,
    steps: [{ ...base.steps[0], dependsOn: ['s2'] }, base.steps[1], base.steps[2]],
  };
  assert.ok(validateScript(script).some((e) => e.includes('not an earlier step')));
});

test('rejects dependsOn pointing at a missing step', () => {
  const script = { ...base, steps: [base.steps[0], { ...base.steps[1], dependsOn: ['nope'] }] };
  assert.ok(validateScript(script).some((e) => e.includes('"nope"')));
});

test('rejects non-positive delay', () => {
  const script = { ...base, steps: [{ id: 's1', type: 'delay', ms: 0 }] };
  assert.ok(validateScript(script).some((e) => e.includes('positive integer')));
});

test('rejects restart of undeclared node', () => {
  const script = { ...base, steps: [{ id: 's1', type: 'restart', node: 'Z' }] };
  assert.ok(validateScript(script).some((e) => e.includes('not a declared node')));
});

test('rejects drop from a node to itself', () => {
  const script = { ...base, steps: [{ id: 's1', type: 'drop', from: 'A', to: 'A', count: 1 }] };
  assert.ok(validateScript(script).some((e) => e.includes('must differ')));
});

test('rejects unknown step type', () => {
  const script = { ...base, steps: [{ id: 's1', type: 'explode' }] };
  assert.ok(validateScript(script).some((e) => e.includes('unknown step type')));
});
