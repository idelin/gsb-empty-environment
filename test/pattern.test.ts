import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compileRule, matchPattern, buildTarget } from '../src/pattern.ts';
import { RuleError } from '../src/index.ts';

const from = (raw: string) => compileRule(raw, raw, 0).from;

test('literal and single-segment wildcard matching', () => {
  assert.deepEqual(matchPattern(from('a/*'), ['a', 'x']), [{ kind: 'one', value: 'x' }]);
  assert.equal(matchPattern(from('a/*'), ['a']), null);
  assert.equal(matchPattern(from('a/*'), ['a', 'x', 'y']), null);
  assert.equal(matchPattern(from('a/b'), ['a', 'x']), null);
});

test('glob matches zero or more segments, greedily with backtracking', () => {
  assert.deepEqual(matchPattern(from('a/**'), ['a']), [{ kind: 'many', values: [] }]);
  assert.deepEqual(matchPattern(from('a/**/z'), ['a', 'x', 'y', 'z']), [
    { kind: 'many', values: ['x', 'y'] },
  ]);
  assert.deepEqual(matchPattern(from('**/z'), ['z']), [{ kind: 'many', values: [] }]);
  assert.equal(matchPattern(from('a/**/z'), ['a', 'x', 'y']), null);
});

test('buildTarget substitutes captures in order', () => {
  const rule = compileRule('a/*/*/b', 'x/*/*', 0);
  const caps = matchPattern(rule.from, ['a', 'm', 'n', 'b']);
  assert.ok(caps);
  assert.deepEqual(buildTarget(rule.to, caps), ['x', 'm', 'n']);
});

test('glob capture expands into multiple target segments', () => {
  const rule = compileRule('src/**', 'dst/**', 0);
  const caps = matchPattern(rule.from, ['src', 'a', 'b']);
  assert.ok(caps);
  assert.deepEqual(buildTarget(rule.to, caps), ['dst', 'a', 'b']);
});

test('capture count mismatch is a RuleError locating the rule', () => {
  assert.throws(() => compileRule('a/*', 'b', 3), (err: unknown) => {
    assert.ok(err instanceof RuleError);
    assert.equal((err as RuleError).ruleIndex, 3);
    assert.match((err as Error).message, /#3/);
    return true;
  });
  assert.throws(() => compileRule('a/*', 'b/**', 0), RuleError);
});

test('wildcard must be a whole segment', () => {
  assert.throws(() => compileRule('a/x*', 'b', 0), RuleError);
});
