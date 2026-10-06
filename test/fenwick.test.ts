import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Fenwick } from '../src/fenwick.ts';
import { mulberry32 } from './random.ts';

function naivePrefix(values: number[], count: number): number {
  let sum = 0;
  for (let i = 0; i < count; i++) sum += values[i];
  return sum;
}

function naiveLowerBound(values: number[], target: number): number {
  let sum = 0;
  for (let i = 0; i < values.length; i++) {
    if (sum + values[i] > target) return i;
    sum += values[i];
  }
  return values.length;
}

test('empty tree', () => {
  const tree = new Fenwick();
  assert.equal(tree.length, 0);
  assert.equal(tree.total(), 0);
  assert.equal(tree.prefixSum(0), 0);
  assert.equal(tree.lowerBound(0), 0);
  assert.equal(tree.lowerBound(100), 0);
});

test('prefix sums and point updates match naive model', () => {
  const rand = mulberry32(42);
  const values = Array.from({ length: 500 }, () => Math.floor(rand() * 100));
  const tree = new Fenwick(values);
  for (let round = 0; round < 200; round++) {
    const count = Math.floor(rand() * (values.length + 1));
    assert.equal(tree.prefixSum(count), naivePrefix(values, count));
    const index = Math.floor(rand() * values.length);
    const delta = Math.floor(rand() * 50);
    values[index] += delta;
    tree.add(index, delta);
  }
  assert.equal(tree.total(), naivePrefix(values, values.length));
});

test('lowerBound matches naive scan including boundaries', () => {
  const rand = mulberry32(7);
  const values = Array.from({ length: 300 }, () => Math.floor(rand() * 20));
  const tree = new Fenwick(values);
  const total = naivePrefix(values, values.length);
  const targets = [-5, -1, 0, 1, total - 1, total, total + 1, total + 100];
  for (let i = 0; i < 200; i++) targets.push(Math.floor(rand() * (total + 50)));
  for (const target of targets) {
    assert.equal(tree.lowerBound(target), naiveLowerBound(values, target), `target=${target}`);
  }
});

test('lowerBound skips zero-height entries', () => {
  const tree = new Fenwick([0, 0, 10, 0, 5]);
  assert.equal(tree.lowerBound(0), 2);
  assert.equal(tree.lowerBound(9), 2);
  assert.equal(tree.lowerBound(10), 4);
  assert.equal(tree.lowerBound(14), 4);
  assert.equal(tree.lowerBound(15), 5);
});

test('reset rebuilds from new values', () => {
  const tree = new Fenwick([1, 2, 3]);
  assert.equal(tree.total(), 6);
  tree.reset([4, 5]);
  assert.equal(tree.length, 2);
  assert.equal(tree.total(), 9);
  assert.equal(tree.prefixSum(1), 4);
  tree.reset([]);
  assert.equal(tree.total(), 0);
});
