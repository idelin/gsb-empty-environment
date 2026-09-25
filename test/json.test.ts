import { test } from 'node:test';
import assert from 'node:assert/strict';
import { canonicalHash, canonicalize, deepClone, deepEqual, getPointer } from '../src/json.ts';

test('canonicalize: 对象键排序（键无序）', () => {
  assert.equal(canonicalize({ b: 1, a: 2 }), canonicalize({ a: 2, b: 1 }));
  assert.equal(canonicalize({ b: 1, a: 2 }), '{"a":2,"b":1}');
});

test('canonicalize: 数组保持顺序', () => {
  assert.notEqual(canonicalize([1, 2, 3]), canonicalize([3, 2, 1]));
  assert.equal(canonicalize([1, 2, 3]), '[1,2,3]');
});

test('canonicalHash: 键序无关、数组顺序有关、嵌套亦然', () => {
  const a = { user: { id: 1, tags: ['x', 'y'] }, n: 2 };
  const reordered = { n: 2, user: { tags: ['x', 'y'], id: 1 } };
  const arrChanged = { n: 2, user: { id: 1, tags: ['y', 'x'] } };
  assert.equal(canonicalHash(a), canonicalHash(reordered));
  assert.notEqual(canonicalHash(a), canonicalHash(arrChanged));
});

test('deepClone 与 deepEqual', () => {
  const src = { a: [1, { b: 2 }], c: null };
  const copy = deepClone(src);
  assert.deepEqual(copy, src);
  assert.notEqual(copy, src);
  assert.notEqual((copy.a as unknown[])[1], (src.a as unknown[])[1]);
  assert.equal(deepEqual({ a: 1 }, { a: 1 }), true);
  assert.equal(deepEqual({ a: 1, b: 2 }, { b: 2, a: 1 }), true);
  assert.equal(deepEqual([1, 2], [2, 1]), false);
});

test('getPointer 支持 RFC 6901 转义与越界缺失', () => {
  const doc = { a: { 'b/c': [{ 'm~n': 42 }] } } as never;
  assert.equal(getPointer(doc, '/a/b~1c/0/m~0n').value, 42);
  assert.equal(getPointer(doc, '/a/missing').found, false);
  assert.equal(getPointer(doc, '/a/b~1c/9').found, false);
  assert.deepEqual(getPointer(doc, '').value, doc);
});
