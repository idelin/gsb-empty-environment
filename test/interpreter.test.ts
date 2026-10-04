import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { FileMeta, Policy } from '../src/types.ts';
import { evalCondition } from '../src/conditions.ts';
import { interpretFile } from '../src/interpreter.ts';

const policy = (rules: Policy['rules'], defaultAction: Policy['defaultAction'] = 'exclude'): Policy => ({
  name: 'test',
  defaultAction,
  rules,
});

test('missing tags keep the decision unknown instead of default-excluding', () => {
  const p = policy([
    { id: 'r1', priority: 10, action: 'include', when: { kind: 'tag', tag: 'keep' } },
  ]);
  const file: FileMeta = { path: 'a.bin', size: 5, version: 1 }; // tags unknown
  const decision = interpretFile(p, file);
  assert.equal(decision.kind, 'unknown');
  if (decision.kind === 'unknown') assert.deepEqual(decision.pendingRuleIds, ['r1']);
});

test('definite false on every rule falls back to the policy default', () => {
  const p = policy([
    { id: 'r1', priority: 10, action: 'include', when: { kind: 'tag', tag: 'keep' } },
  ]);
  const file: FileMeta = { path: 'a.bin', size: 5, version: 1, tags: ['other'] };
  assert.deepEqual(interpretFile(p, file), { kind: 'default', action: 'exclude' });
});

test('higher priority rule wins over a lower matching rule', () => {
  const p = policy([
    { id: 'lo', priority: 1, action: 'include', when: { kind: 'path', glob: '**' } },
    { id: 'hi', priority: 9, action: 'exclude', when: { kind: 'path', glob: 'tmp/**' } },
  ]);
  const decision = interpretFile(p, { path: 'tmp/x', size: 1, version: 1, tags: [] });
  assert.deepEqual(decision, { kind: 'decided', action: 'exclude', ruleId: 'hi' });
});

test('an unknown higher-priority rule blocks a definite lower-priority match', () => {
  const p = policy([
    {
      id: 'hi',
      priority: 9,
      action: 'exclude',
      when: { kind: 'all', conds: [{ kind: 'path', glob: '**' }, { kind: 'tag', tag: 'junk' }] },
    },
    { id: 'lo', priority: 1, action: 'include', when: { kind: 'path', glob: '**' } },
  ]);
  const file: FileMeta = { path: 'x', size: 1, version: 1 }; // tags unknown
  const decision = interpretFile(p, file);
  assert.equal(decision.kind, 'unknown');
  if (decision.kind === 'unknown') assert.deepEqual(decision.pendingRuleIds, ['hi']);
});

test('size bounds are inclusive at both ends', () => {
  const cond = { kind: 'size', min: 100, max: 200 } as const;
  const at = (size?: number) => evalCondition(cond, { path: 'f', size });
  assert.equal(at(100), 'true');
  assert.equal(at(200), 'true');
  assert.equal(at(99), 'false');
  assert.equal(at(201), 'false');
  assert.equal(at(undefined), 'unknown');
});

test('version bounds are inclusive and unknown when version is missing', () => {
  const cond = { kind: 'version', min: 3 } as const;
  assert.equal(evalCondition(cond, { path: 'f', version: 3 }), 'true');
  assert.equal(evalCondition(cond, { path: 'f', version: 2 }), 'false');
  assert.equal(evalCondition(cond, { path: 'f' }), 'unknown');
});

test('negated tag condition is unknown when tags are missing', () => {
  const cond = { kind: 'not', cond: { kind: 'tag', tag: 'junk' } } as const;
  assert.equal(evalCondition(cond, { path: 'f' }), 'unknown');
  assert.equal(evalCondition(cond, { path: 'f', tags: ['junk'] }), 'false');
  assert.equal(evalCondition(cond, { path: 'f', tags: ['keep'] }), 'true');
});

test('negated size range complements the interval', () => {
  const cond = { kind: 'not', cond: { kind: 'size', min: 10, max: 20 } } as const;
  assert.equal(evalCondition(cond, { path: 'f', size: 15 }), 'false');
  assert.equal(evalCondition(cond, { path: 'f', size: 9 }), 'true');
  assert.equal(evalCondition(cond, { path: 'f', size: 21 }), 'true');
  assert.equal(evalCondition(cond, { path: 'f' }), 'unknown');
});

test('any-of is unknown when no branch is true but one is unknown', () => {
  const cond = {
    kind: 'all',
    conds: [
      { kind: 'any', conds: [
        { kind: 'tag', tag: 'a' },
        { kind: 'size', min: 1000 },
      ] },
      { kind: 'path', glob: 'docs/**' },
    ],
  } as const;
  assert.equal(evalCondition(cond, { path: 'docs/x', size: 5 }), 'unknown'); // tags missing
  assert.equal(evalCondition(cond, { path: 'docs/x', size: 5, tags: [] }), 'false');
  assert.equal(evalCondition(cond, { path: 'docs/x', size: 2000, tags: [] }), 'true');
});
