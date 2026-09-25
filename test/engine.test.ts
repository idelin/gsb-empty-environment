import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runRules, type Rule } from '../src/engine.ts';
import { canonicalize, getAt, type JsonValue } from '../src/json.ts';

const replaceValue = (
  name: string,
  priority: number,
  from: JsonValue,
  to: JsonValue,
): Rule => ({
  name,
  priority,
  precondition: ({ value }) => value === from,
  fix: () => to,
});

test('fixpoint: rules apply until no precondition holds, steps journaled with paths', () => {
  const input = { items: [{ name: 'x' }, { name: 'ok' }, { name: 'x' }] };
  const result = runRules(input, [replaceValue('A:x->y', 1, 'x', 'y')]);

  assert.equal(result.status, 'fixpoint');
  assert.equal(result.steps.length, 2);
  assert.deepEqual(
    result.steps.map((s) => [s.seq, s.rule, s.path, s.before, s.after]),
    [
      [1, 'A:x->y', '/items/0/name', 'x', 'y'],
      [2, 'A:x->y', '/items/2/name', 'x', 'y'],
    ],
  );
  assert.deepEqual(result.output, {
    items: [{ name: 'y' }, { name: 'ok' }, { name: 'y' }],
  });
});

test('priority: higher-priority rule fires first regardless of rule list order', () => {
  const input = { a: 'x', b: 'x' };
  const low: Rule = {
    name: 'low',
    priority: 5,
    precondition: ({ value }) => value === 'x',
    fix: () => 'z',
  };
  const high: Rule = {
    name: 'high',
    priority: 1,
    precondition: ({ path, value }) => path.join('/') === 'b' && value === 'x',
    fix: () => 'y',
  };
  const result = runRules(input, [low, high]);
  assert.equal(result.steps[0].rule, 'high');
  assert.equal(result.steps[0].path, '/b');
  assert.deepEqual(result.output, { a: 'z', b: 'y' });
});

test('precondition: rule never fires when its precondition is unsatisfied', () => {
  const input = { v: 'untouched' };
  const guarded: Rule = {
    name: 'guarded',
    priority: 1,
    precondition: ({ value }) => value === 'x',
    fix: () => {
      throw new Error('must not execute');
    },
  };
  const result = runRules(input, [guarded]);
  assert.equal(result.status, 'fixpoint');
  assert.equal(result.steps.length, 0);
  assert.deepEqual(result.output, input);
});

test('oscillation: rule A (x->y) vs rule B (y->x) is detected with evidence', () => {
  const input = { v: 'x' };
  const result = runRules(input, [
    replaceValue('A:x->y', 1, 'x', 'y'),
    replaceValue('B:y->x', 2, 'y', 'x'),
  ]);

  assert.equal(result.status, 'oscillation');
  const ev = result.oscillation!;
  assert.deepEqual(ev.rulesInvolved, ['A:x->y', 'B:y->x']);
  assert.equal(ev.cycleStartSeq, 1);
  assert.deepEqual(
    ev.cycleSteps.map((s) => [s.seq, s.rule, s.path, s.before, s.after]),
    [
      [1, 'A:x->y', '/v', 'x', 'y'],
      [2, 'B:y->x', '/v', 'y', 'x'],
    ],
  );
  // The engine stopped instead of looping forever; journal holds exactly one cycle.
  assert.equal(result.steps.length, 2);
});

test('oscillation: a no-op fix is a length-1 cycle and is also detected', () => {
  const noop: Rule = {
    name: 'noop',
    priority: 1,
    precondition: ({ value }) => value === 'x',
    fix: (value) => value,
  };
  const result = runRules({ v: 'x' }, [noop]);
  assert.equal(result.status, 'oscillation');
  assert.equal(result.oscillation!.cycleSteps.length, 1);
});

test('conflict: distinct rules with equal priority at the same path stop the run', () => {
  const input = { v: 'x' };
  const r1: Rule = {
    name: 'to-y',
    priority: 1,
    precondition: ({ value }) => value === 'x',
    fix: () => 'y',
  };
  const r2: Rule = {
    name: 'to-z',
    priority: 1,
    precondition: ({ value }) => value === 'x',
    fix: () => 'z',
  };
  const result = runRules(input, [r1, r2]);

  assert.equal(result.status, 'conflict');
  assert.equal(result.steps.length, 0);
  const ev = result.conflict!;
  assert.equal(ev.path, '/v');
  assert.equal(ev.value, 'x');
  assert.deepEqual(ev.competitors, [
    { rule: 'to-y', priority: 1 },
    { rule: 'to-z', priority: 1 },
  ]);
});

test('budget: run terminates when the step budget is exhausted', () => {
  const grow: Rule = {
    name: 'grow',
    priority: 1,
    precondition: ({ path }) => path.length === 0,
    fix: (value) => [...(value as JsonValue[]), 'x'],
  };
  const result = runRules([], [grow], { budget: 5 });
  assert.equal(result.status, 'budget-exhausted');
  assert.equal(result.steps.length, 5);
  assert.deepEqual(result.output, ['x', 'x', 'x', 'x', 'x']);
});

test('rollback: restores the original input object by identity, not a normalized copy', () => {
  const input = { b: 'x', a: [1, 'x'] };
  const result = runRules(input, [replaceValue('A:x->y', 1, 'x', 'y')], {
    budget: 10,
  });
  assert.equal(result.steps.length, 2);

  const restored = result.rollback();
  assert.equal(restored, input); // identity: the very same object
  assert.deepEqual(restored, { b: 'x', a: [1, 'x'] });
  // Input was never mutated and output is a separate tree.
  assert.deepEqual(input, { b: 'x', a: [1, 'x'] });
  assert.notEqual(result.output, input);
  assert.deepEqual(result.output, { b: 'y', a: [1, 'y'] });
});

test('arrays are ordered: fixes apply in index order', () => {
  const input = ['x', 'skip', 'x', 'x'];
  const result = runRules(input, [replaceValue('A:x->y', 1, 'x', 'y')]);
  assert.deepEqual(
    result.steps.map((s) => s.path),
    ['/0', '/2', '/3'],
  );
});

test('object keys are unordered: key insertion order affects neither output identity nor cycle detection', () => {
  const rules = [
    replaceValue('A:x->y', 1, 'x', 'y'),
    replaceValue('B:y->x', 2, 'y', 'x'),
  ];
  const first = runRules({ p: 'x', q: 1 }, rules);
  const second = runRules({ q: 1, p: 'x' }, rules);

  assert.equal(first.status, 'oscillation');
  assert.equal(second.status, 'oscillation');
  assert.equal(first.oscillation!.stateHash, second.oscillation!.stateHash);
  assert.equal(canonicalize(first.output), canonicalize(second.output));
});

test('paths locate errors precisely in nested structures', () => {
  const input = { level1: { level2: [{ deep: 'keep' }, { deep: 'x' }] } };
  const result = runRules(input, [replaceValue('A:x->y', 1, 'x', 'y')]);
  assert.equal(result.steps.length, 1);
  const step = result.steps[0];
  assert.equal(step.path, '/level1/level2/1/deep');
  assert.equal(getAt(result.output, step.segments), 'y');
});

test('journal is reversible: undoing steps in reverse restores the start state', () => {
  const input = { v: ['x', 'x'] };
  const result = runRules(input, [replaceValue('A:x->y', 1, 'x', 'y')]);
  assert.equal(result.steps.length, 2);

  let restored = result.output;
  for (const step of [...result.steps].reverse()) {
    // every step recorded enough information to be undone
    restored = applyUndo(restored, step.segments, step.before);
  }
  assert.equal(canonicalize(restored), canonicalize(input));
});

function applyUndo(root: JsonValue, path: readonly (string | number)[], before: JsonValue): JsonValue {
  if (path.length === 0) return before;
  const [head, ...rest] = path;
  if (Array.isArray(root) && typeof head === 'number') {
    const copy = root.slice();
    copy[head] = applyUndo(root[head], rest, before);
    return copy;
  }
  const obj = root as Record<string, JsonValue>;
  return { ...obj, [head as string]: applyUndo(obj[head as string], rest, before) };
}
