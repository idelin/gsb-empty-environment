import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse } from '../src/jsonpath/parser.ts';
import { BudgetTracker } from '../src/jsonpath/budget.ts';
import { BudgetExceededError, ParseError } from '../src/jsonpath/errors.ts';

test('解析字段、下标、通配', () => {
  const q = parse('$.store.book[0].title');
  assert.deepEqual(q.segments, [
    { kind: 'child', key: 'store' },
    { kind: 'child', key: 'book' },
    { kind: 'index', index: 0 },
    { kind: 'child', key: 'title' },
  ]);
  assert.deepEqual(parse('$.*').segments, [{ kind: 'wildcard' }]);
  assert.deepEqual(parse('$[*]').segments, [{ kind: 'wildcard' }]);
});

test('带斜杠与特殊字符的键必须用括号记法', () => {
  const q = parse(`$['a/b']['x y']["q\\"u"]`);
  assert.deepEqual(q.segments, [
    { kind: 'child', key: 'a/b' },
    { kind: 'child', key: 'x y' },
    { kind: 'child', key: 'q"u' },
  ]);
});

test('切片与负下标', () => {
  assert.deepEqual(parse('$.a[1:8:2]').segments, [
    { kind: 'child', key: 'a' },
    { kind: 'slice', start: 1, end: 8, step: 2 },
  ]);
  assert.deepEqual(parse('$.a[-1]').segments, [
    { kind: 'child', key: 'a' },
    { kind: 'index', index: -1 },
  ]);
  assert.deepEqual(parse('$.a[::-1]').segments, [
    { kind: 'child', key: 'a' },
    { kind: 'slice', start: null, end: null, step: -1 },
  ]);
  assert.throws(() => parse('$.a[::0]'), /step/);
});

test('递归下降', () => {
  assert.deepEqual(parse('$..author').segments, [{ kind: 'recursive' }, { kind: 'child', key: 'author' }]);
  assert.deepEqual(parse('$..*').segments, [{ kind: 'recursive' }, { kind: 'wildcard' }]);
  assert.deepEqual(parse('$..[0]').segments, [{ kind: 'recursive' }, { kind: 'index', index: 0 }]);
});

test('谓词：比较、逻辑、存在性、根引用', () => {
  const q = parse(`$.a[?(@.x >= 2 && (@.y != 's' || !@.z))].b`);
  const filter = q.segments[1];
  assert.equal(filter.kind, 'filter');
  const rootRef = parse('$.a[?(@.x < $.limit)]');
  assert.equal(rootRef.segments[1]!.kind, 'filter');
  const exists = parse('$.a[?(@.x)]');
  assert.equal(exists.segments[1]!.kind, 'filter');
});

test('解析错误携带查询串位置', () => {
  try {
    parse('$.a[');
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof ParseError);
    assert.equal(typeof e.position, 'number');
    assert.match(e.message, /position \d+/);
  }
  assert.throws(() => parse('a.b'), ParseError);
  assert.throws(() => parse('$.a[?(@.x == )]'), ParseError);
});

test('解析阶段预算', () => {
  const budget = new BudgetTracker({ parse: 3 });
  assert.throws(() => parse('$.a.b.c.d.e', budget), (e: unknown) => {
    assert.ok(e instanceof BudgetExceededError);
    assert.equal(e.phase, 'parse');
    return true;
  });
});
