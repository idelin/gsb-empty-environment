import { test } from 'node:test';
import assert from 'node:assert/strict';
import { topK } from '../src/topk.ts';

test('returns the k largest in descending order', () => {
  const sums = Float64Array.from([5, 1, 9, 3, 7, 0, 2]);
  const result = topK(sums, 3);
  assert.deepEqual(
    result.map((r) => r.cell),
    [2, 4, 0],
  );
  assert.deepEqual(
    result.map((r) => r.weight),
    [9, 7, 5],
  );
});

test('k larger than non-zero entries returns only non-zero cells', () => {
  const sums = Float64Array.from([0, 4, 0, 2]);
  const result = topK(sums, 10);
  assert.equal(result.length, 2);
  assert.equal(result[0].cell, 1);
  assert.equal(result[1].cell, 3);
});

test('k=0 and empty input are safe', () => {
  assert.deepEqual(topK(Float64Array.from([1, 2]), 0), []);
  assert.deepEqual(topK(new Float64Array(0), 5), []);
  assert.deepEqual(topK(new Float64Array(4), 3), []);
});

test('ties broken deterministically by cell index', () => {
  const sums = Float64Array.from([3, 3, 3]);
  const result = topK(sums, 2);
  assert.equal(result.length, 2);
  assert.equal(result[0].weight, 3);
  assert.equal(result[1].weight, 3);
});
