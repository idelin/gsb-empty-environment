import { test } from 'node:test';
import assert from 'node:assert/strict';
import { hasField, deepEqual } from '../src/index.ts';

test('presence is key-existence, not truthiness', () => {
  const obj = { a: false, b: null, c: 0, d: '', e: [], f: {}, g: undefined };
  for (const k of Object.keys(obj)) {
    assert.equal(hasField(obj, k), true, `${k} must count as present`);
  }
  assert.equal(hasField(obj, 'missing'), false);
  assert.equal(hasField({}, 'a'), false);
});

test('presence rejects non-object containers', () => {
  assert.equal(hasField(null, 'a'), false);
  assert.equal(hasField(['a'], '0'), false);
  assert.equal(hasField(42, 'a'), false);
});

test('deepEqual handles json values', () => {
  assert.ok(deepEqual({ a: [1, { b: null }] }, { a: [1, { b: null }] }));
  assert.ok(!deepEqual({ a: 1 }, { a: 1, b: 2 }));
  assert.ok(!deepEqual([1], { 0: 1 }));
  assert.ok(!deepEqual(0, false));
  assert.ok(deepEqual(NaN, NaN));
});
