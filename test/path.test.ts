import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parsePath, formatPath, escapeSegment, PathSyntaxError } from '../src/index.ts';

test('parses plain segments', () => {
  assert.deepEqual(parsePath('a/b/c'), ['a', 'b', 'c']);
  assert.deepEqual(parsePath(''), []);
  assert.deepEqual(parsePath('a'), ['a']);
});

test('escapes slashes and backslashes inside keys', () => {
  assert.deepEqual(parsePath('a\\/b/c'), ['a/b', 'c']);
  assert.deepEqual(parsePath('a\\\\b'), ['a\\b']);
  assert.deepEqual(parsePath('x\\/y\\\\z'), ['x/y\\z']);
});

test('round-trips keys containing separators', () => {
  for (const segs of [['a/b'], ['a\\b'], ['a/b\\c', 'd'], ['**'], ['*'], ['a', '']]) {
    assert.deepEqual(parsePath(formatPath(segs)), segs);
  }
});

test('formats root as empty string', () => {
  assert.equal(formatPath([]), '');
  assert.equal(escapeSegment('a/b'), 'a\\/b');
});

test('rejects unsupported escapes with offset', () => {
  assert.throws(() => parsePath('a\\tb'), (err: unknown) => {
    assert.ok(err instanceof PathSyntaxError);
    assert.equal((err as PathSyntaxError).offset, 1);
    assert.match((err as Error).message, /unsupported escape/);
    return true;
  });
});
