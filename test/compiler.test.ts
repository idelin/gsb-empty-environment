import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, formatPath } from '../src/index.ts';

const doc = {
  store: {
    book: [
      { title: 'A', price: 5, tags: ['x'], stock: 0 },
      { title: 'B', price: 12, tags: ['y', 'z'], stock: 3 },
      { title: 'C', price: 8, tags: [], stock: 7 },
    ],
    bicycle: { color: 'red', price: 19 },
  },
};

test('child and index navigation with evidence paths', () => {
  const m = compile('$.store.book[1].title').run(doc);
  assert.equal(m.length, 1);
  assert.equal(m[0].value, 'B');
  assert.equal(formatPath(m[0].path), '$.store.book[1].title');
});

test('wildcard over arrays and objects', () => {
  assert.deepEqual(
    compile('$.store.book[*].title').run(doc).map((x) => x.value),
    ['A', 'B', 'C'],
  );
  const keys = compile('$.store[*]').run(doc).map((x) => x.path[1]);
  assert.deepEqual(keys, ['book', 'bicycle']);
});

test('integer comparisons and boolean operators in filters', () => {
  assert.deepEqual(
    compile('$.store.book[?(@.price < 10)].title').run(doc).map((x) => x.value),
    ['A', 'C'],
  );
  assert.deepEqual(
    compile('$.store.book[?(@.price >= 5 && @.price <= 8)].title').run(doc).map((x) => x.value),
    ['A', 'C'],
  );
  assert.deepEqual(
    compile('$.store.book[?(@.price < 6 || @.price > 10)].title').run(doc).map((x) => x.value),
    ['A', 'B'],
  );
  assert.deepEqual(
    compile('$.store.book[?(!(@.stock == 0))].title').run(doc).map((x) => x.value),
    ['B', 'C'],
  );
});

test('in operator with finite sets', () => {
  assert.deepEqual(
    compile("$.store.book[?(@.title in ['A', 'C'])].price").run(doc).map((x) => x.value),
    [5, 8],
  );
  assert.deepEqual(
    compile('$.store.book[?(@.stock in [0, 3])].title').run(doc).map((x) => x.value),
    ['A', 'B'],
  );
});

test('recursive descent finds matches at any depth', () => {
  const prices = compile('$..price').run(doc);
  assert.deepEqual(
    prices.map((x) => x.value).sort((a, b) => (a as number) - (b as number)),
    [5, 8, 12, 19],
  );
  assert.deepEqual(
    compile('$..book[?(@.stock == 7)].title').run(doc).map((x) => x.value),
    ['C'],
  );
});

test('root-only expression matches the root itself', () => {
  const m = compile('$').run(doc);
  assert.equal(m.length, 1);
  assert.equal(m[0].value, doc);
  assert.equal(formatPath(m[0].path), '$');
});

test('missing branches yield no matches and no errors', () => {
  assert.deepEqual(compile('$.nope.deeper[3]').run(doc), []);
  assert.deepEqual(compile('$.store.book[9]').run(doc), []);
  assert.deepEqual(compile('$.store.book.title').run(doc), [], 'no implicit mapping over arrays');
});

test('weird property names via quoted segments', () => {
  const weird = { 'a b': { '': 1 }, '构造': 2 };
  assert.equal(compile("$.'a b'.''").run(weird)[0].value, 1);
  assert.equal(compile('$.构造').run(weird)[0].value, 2);
});

test('cyclic structures terminate via the ancestor guard', () => {
  const a: Record<string, unknown> = { name: 'a' };
  const b: Record<string, unknown> = { name: 'b', back: a };
  a.next = b;
  const names = compile('$..name').run(a).map((x) => x.value);
  assert.deepEqual(names.sort(), ['a', 'b']);
});
