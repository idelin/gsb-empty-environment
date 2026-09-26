import { test } from 'node:test';
import assert from 'node:assert/strict';
import { minimumHittingSet } from '../src/index.ts';

test('exact minimum on a known instance', () => {
  // s0={0}, s1={0,1}, s2={1,2}: optimum is {0,1} of size 2.
  const result = minimumHittingSet([[0], [0, 1], [1, 2]], 3);
  assert.equal(result.optimal, true);
  assert.equal(result.selected.length, 2);
  assert.equal(result.lowerBound, 2);
});

test('shared candidate covers all sets', () => {
  const result = minimumHittingSet([[0, 1], [0, 2], [0, 3]], 4);
  assert.equal(result.optimal, true);
  assert.deepEqual(result.selected, [0]);
});

test('budget exhaustion returns unproven bounds instead of a false optimum', () => {
  const sets = [[0, 1], [2, 3], [4, 5], [6, 7], [8, 9]];
  const result = minimumHittingSet(sets, 10, 0);
  assert.equal(result.optimal, false);
  assert.ok(result.lowerBound <= result.selected.length);
  assert.ok(result.selected.length === result.upperBound);
  assert.ok(result.lowerBound >= 1);
});

test('empty universe is trivially optimal', () => {
  const result = minimumHittingSet([], 0);
  assert.equal(result.optimal, true);
  assert.deepEqual(result.selected, []);
});

test('infeasible input (failed branch without errors) throws', () => {
  assert.throws(() => minimumHittingSet([[0], []], 1), /infeasible/);
});
