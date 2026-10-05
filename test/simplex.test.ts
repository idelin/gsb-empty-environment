import { test } from 'node:test';
import assert from 'node:assert/strict';
import { solveLpMax, solveLpMin } from '../src/simplex.ts';

test('单纯形：基本最大化', () => {
  // max x+y, x+2y<=4, 4x+2y<=12 -> (8/3, 2/3), 10/3
  const res = solveLpMax([[1, 2], [4, 2]], [4, 12], [1, 1]);
  assert.equal(res.status, 'optimal');
  assert.ok(Math.abs(res.objective - 10 / 3) < 1e-7);
});

test('单纯形：负右端项（>= 约束）与最小化', () => {
  // min 3x+2y, x+y>=4, x+3y>=6 -> (0, 4), 8
  const res = solveLpMin([[-1, -1], [-1, -3]], [-4, -6], [3, 2]);
  assert.equal(res.status, 'optimal');
  assert.ok(Math.abs(res.objective - 8) < 1e-7);
});

test('单纯形：不可行与无界', () => {
  assert.equal(solveLpMax([[-1], [1]], [-2, 1], [1]).status, 'infeasible');
  assert.equal(solveLpMax([[-1]], [0], [1]).status, 'unbounded');
});
