import { test } from 'node:test';
import assert from 'node:assert/strict';
import { canonical, deepEqual, getPath, parsePath } from '../src/json.ts';

test('对象键无序：键序不同规范化哈希相同', () => {
  const a = { x: 1, y: { p: 'a', q: [1, 2] } };
  const b = { y: { q: [1, 2], p: 'a' }, x: 1 };
  assert.equal(canonical(a), canonical(b));
  assert.ok(deepEqual(a, b));
});

test('数组有序：顺序不同则哈希不同', () => {
  assert.notEqual(canonical({ a: [1, 2] }), canonical({ a: [2, 1] }));
  assert.ok(!deepEqual({ a: [1, 2] }, { a: [2, 1] }));
});

test('路径解析与读取定位到具体路径', () => {
  assert.deepEqual(parsePath('$.a.b[0].c'), ['a', 'b', 0, 'c']);
  const v = { a: { b: [{ c: 42 }] } };
  assert.deepEqual(getPath(v, parsePath('$.a.b[0].c')), { found: true, value: 42 });
  assert.equal(getPath(v, parsePath('$.a.b[1].c')).found, false);
  assert.equal(getPath(v, parsePath('$.a.x')).found, false);
});
