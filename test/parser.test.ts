import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parsePath, LIMITS } from '../src/parser.ts';
import { PathError } from '../src/errors.ts';

test('parses key, index, wildcard, descend and string-key segments', () => {
  const ast = parsePath(`a.b[0]['c d'].*..e`);
  assert.deepEqual(
    ast.segments.map((s) => s.kind),
    ['key', 'key', 'index', 'key', 'wildcard', 'descend'],
  );
  assert.equal(ast.segments[3]!.kind === 'key' && ast.segments[3]!.name, 'c d');
});

test('parses filters with integer conditions, sets and boolean ops', () => {
  const ast = parsePath(`items[?(@.price >= 10 && @.tag in {'a','b'} || !(@.n == 0))]`);
  const seg = ast.segments[1]!;
  assert.equal(seg.kind, 'filter');
});

test('rejects __proto__, constructor and prototype at parse time', () => {
  for (const bad of [
    'a.__proto__',
    'a.constructor',
    `a['__proto__']`,
    `a["constructor"]`,
    'a..prototype',
    'a[?(@.__proto__ == 1)]',
    `a[?(@['constructor'] == 1)]`,
  ]) {
    assert.throws(
      () => parsePath(bad),
      (e: unknown) => e instanceof PathError && e.code === 'FORBIDDEN_PROPERTY' && e.userPath === bad,
      bad,
    );
  }
});

test('excessive depth is a parse-time error', () => {
  const deep = Array.from({ length: LIMITS.maxSegments + 1 }, (_, i) => `k${i}`).join('.');
  assert.throws(
    () => parsePath(deep),
    (e: unknown) => e instanceof PathError && e.code === 'DEPTH_LIMIT',
  );
  const nested = `a[?(${ '('.repeat(LIMITS.maxFilterDepth + 1) }@.x == 1${ ')'.repeat(LIMITS.maxFilterDepth + 1) })]`;
  assert.throws(
    () => parsePath(nested),
    (e: unknown) => e instanceof PathError && e.code === 'DEPTH_LIMIT',
  );
  const long = 'a'.repeat(LIMITS.maxSourceLength + 1);
  assert.throws(
    () => parsePath(long),
    (e: unknown) => e instanceof PathError && e.code === 'DEPTH_LIMIT',
  );
});

test('only integer literals are allowed in conditions', () => {
  assert.throws(() => parsePath('a[?(@.x > 1.5)]'), PathError);
  assert.throws(() => parsePath('a[?(@.x > 1e3)]'), PathError);
  assert.doesNotThrow(() => parsePath('a[?(@.x > -42)]'));
});

test('rejects calls, arithmetic and malformed syntax', () => {
  for (const bad of ['a.b()', 'a[?(@.x + 1 == 2)]', 'a[?(@.x = 1)]', 'a[', '', '.']) {
    assert.throws(() => parsePath(bad), PathError, bad);
  }
});

test('errors carry the user path, not internal state', () => {
  try {
    parsePath('a.constructor.b');
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof PathError);
    assert.equal(e.userPath, 'a.constructor.b');
    assert.match(e.message, /constructor/);
  }
});
