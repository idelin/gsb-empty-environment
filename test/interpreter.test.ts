import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/jsonpath/compiler.ts';
import { evaluate } from '../src/jsonpath/interpreter.ts';
import { canonical } from '../src/jsonpath/path.ts';
import { BudgetTracker } from '../src/jsonpath/budget.ts';
import { BudgetExceededError } from '../src/jsonpath/errors.ts';

const doc = {
  store: {
    book: [
      { title: 'A', price: 5 },
      { title: 'B', price: 12 },
      { title: 'C', price: 8 },
    ],
    bicycle: { color: 'red', price: 19 },
  },
  'a/b': { 'c/d': 42 },
  empty: [],
};

function paths(q: string, d: unknown = doc): string[] {
  return evaluate(d as never, compile(q)).map((m) => canonical(m.path));
}

test('字段与下标命中并携带规范路径', () => {
  assert.deepEqual(paths('$.store.book[1].title'), ['$["store"]["book"][1]["title"]']);
  assert.deepEqual(paths('$.store.book[-1].title'), ['$["store"]["book"][2]["title"]']);
});

test('带斜杠键精确定位', () => {
  assert.deepEqual(paths(`$['a/b']['c/d']`), ['$["a/b"]["c/d"]']);
  const [m] = evaluate(doc as never, compile(`$['a/b']['c/d']`));
  assert.equal(m!.value, 42);
});

test('通配与切片', () => {
  assert.deepEqual(paths('$.store.book[*].price'), [
    '$["store"]["book"][0]["price"]',
    '$["store"]["book"][1]["price"]',
    '$["store"]["book"][2]["price"]',
  ]);
  assert.deepEqual(paths('$.store.book[0:2].title'), [
    '$["store"]["book"][0]["title"]',
    '$["store"]["book"][1]["title"]',
  ]);
  assert.deepEqual(paths('$.store.book[::-1].title'), [
    '$["store"]["book"][2]["title"]',
    '$["store"]["book"][1]["title"]',
    '$["store"]["book"][0]["title"]',
  ]);
  assert.deepEqual(paths('$.store.book[-2:].title'), [
    '$["store"]["book"][1]["title"]',
    '$["store"]["book"][2]["title"]',
  ]);
});

test('递归下降与同路径去重', () => {
  const d = { x: { x: { x: 1 } } };
  // $..x..x 会从多个祖先重复命中同一路径，结果必须去重
  const got = paths('$..x..x', d);
  assert.deepEqual(got, ['$["x"]["x"]', '$["x"]["x"]["x"]']);
  assert.deepEqual(paths('$..x', d), ['$["x"]', '$["x"]["x"]', '$["x"]["x"]["x"]']);
});

test('谓词过滤', () => {
  assert.deepEqual(paths('$.store.book[?(@.price < 10)].title'), [
    '$["store"]["book"][0]["title"]',
    '$["store"]["book"][2]["title"]',
  ]);
  assert.deepEqual(paths('$.store.book[?(@.price >= 5 && @.price <= 8)].title'), [
    '$["store"]["book"][0]["title"]',
    '$["store"]["book"][2]["title"]',
  ]);
  assert.deepEqual(paths(`$.store.book[?(@.title == 'B')].price`), ['$["store"]["book"][1]["price"]']);
});

test('空结果精确返回空数组', () => {
  assert.deepEqual(paths('$.missing.deep[?(@.x)]'), []);
  assert.deepEqual(paths('$.empty[*]'), []);
  assert.deepEqual(paths('$.store.book[?(@.price > 1000)]'), []);
});

test('深树不爆栈', () => {
  const depth = 5000;
  let node: Record<string, unknown> = { v: 'leaf' };
  for (let i = 0; i < depth; i++) node = { next: node };
  const got = paths('$..v', node);
  assert.equal(got.length, 1);
  assert.ok(got[0]!.startsWith('$["next"]'));
});

test('展开与比较预算', () => {
  const b1 = new BudgetTracker({ expand: 2 });
  assert.throws(() => evaluate(doc as never, compile('$.store.book[*].price'), { budget: b1 }), (e: unknown) => {
    assert.ok(e instanceof BudgetExceededError);
    assert.equal(e.phase, 'expand');
    assert.ok(e.path !== undefined); // 错误定位到具体路径
    return true;
  });
  const b2 = new BudgetTracker({ compare: 1 });
  assert.throws(
    () => evaluate(doc as never, compile('$.store.book[?(@.price > 1)]'), { budget: b2 }),
    BudgetExceededError,
  );
});
