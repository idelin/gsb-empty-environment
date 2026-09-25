import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse, compile } from '../src/index.ts';
import { ParseError } from '../src/errors.ts';

test('parses basic child/index/wildcard/filter/descent expressions', () => {
  for (const expr of [
    '$',
    '$.a',
    '$.a.b.c',
    "$.'a b'.c",
    '$[0].a',
    '$.items[*]',
    '$.items[?(@.id == 1)]',
    "$.items[?(@.tag in ['a', 'b'])]",
    '$.items[?(@.n >= 1 && @.n <= 10 || !@.flag)]',
    '$.items[?(exists(@.meta))]',
    '$..id',
    '$.a..b[?(@ == undefined)]',
  ]) {
    assert.doesNotThrow(() => parse(expr), expr);
  }
});

test('denies __proto__, constructor and prototype at parse time', () => {
  for (const expr of [
    '$.__proto__',
    '$.constructor',
    '$.prototype',
    '$.a.__proto__.b',
    '$..__proto__',
    '$[?(@.constructor == 1)]',
    '$[?(@.__proto__.x)]',
    '$[?(exists(@.prototype))]',
  ]) {
    assert.throws(() => compile(expr), ParseError, expr);
  }
});

test('denied-key parse errors reference the user expression, not internals', () => {
  try {
    compile('$.a.__proto__.b');
    assert.unreachable('should have thrown');
  } catch (err) {
    assert.ok(err instanceof ParseError);
    const e = err as InstanceType<typeof ParseError>;
    assert.equal(e.expression, '$.a.__proto__.b');
    assert.match(e.message, /\$\.a\.__proto__\.b/);
    assert.match(e.message, /"__proto__"/);
  }
});

test('excessive depth fails at parse time', () => {
  const deep = '$' + '.a'.repeat(33);
  assert.throws(() => compile(deep), /path too deep/);
  const atLimit = '$' + '.a'.repeat(32);
  assert.doesNotThrow(() => compile(atLimit));
});

test('recursive descent count is limited at parse time', () => {
  assert.throws(() => compile('$..a..b..c..d..e'), /too many recursive descents/);
  assert.doesNotThrow(() => compile('$..a..b'));
});

test('in-set size is limited at parse time', () => {
  const big = `$[?(@ in [${Array.from({ length: 65 }, (_, i) => i).join(',')}])]`;
  assert.throws(() => compile(big), /set too large/);
});

test('overlong expressions are rejected at parse time', () => {
  assert.throws(() => compile(`$.${'a'.repeat(600)}`), /too long/);
});

test('syntax errors carry the expression and a position', () => {
  for (const bad of ['', '$.a.', '$[', '$.a[?(@ ==)]', '$[?(@ in [])]', '$.a b', '$[?(@ = 1)]']) {
    assert.throws(() => compile(bad), ParseError, JSON.stringify(bad));
  }
  try {
    compile('$.a.');
    assert.unreachable();
  } catch (err) {
    const e = err as InstanceType<typeof ParseError>;
    assert.equal(typeof e.position, 'number');
  }
});

test('bare literal is not a valid condition', () => {
  assert.throws(() => compile('$[?(1)]'), ParseError);
  assert.doesNotThrow(() => compile('$[?(@ == 1)]'));
});
