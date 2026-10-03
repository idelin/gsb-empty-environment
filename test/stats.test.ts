import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mean,
  stdev,
  logGamma,
  gammaQ,
  chiSquareUniform,
  chiSquarePValue,
} from '../src/stats.ts';

test('mean / stdev 基础性质', () => {
  assert.equal(mean([2, 4, 6]), 4);
  assert.equal(stdev([5, 5, 5]), 0);
  // 总体标准差：sqrt(((1-3)^2+(3-3)^2+(5-3)^2)/3) = sqrt(8/3)
  assert.ok(Math.abs(stdev([1, 3, 5]) - Math.sqrt(8 / 3)) < 1e-12);
});

test('logGamma 已知值', () => {
  assert.ok(Math.abs(logGamma(1)) < 1e-12);
  assert.ok(Math.abs(logGamma(0.5) - Math.log(Math.sqrt(Math.PI))) < 1e-12);
  assert.ok(Math.abs(logGamma(5) - Math.log(24)) < 1e-12);
});

test('gammaQ 特例：Q(1,x) = e^-x', () => {
  assert.ok(Math.abs(gammaQ(1, 1) - Math.exp(-1)) < 1e-10);
  assert.ok(Math.abs(gammaQ(1, 2) - Math.exp(-2)) < 1e-10);
});

test('卡方 p 值对照 R 分位数表', () => {
  // qchisq(0.95, df=1) = 3.841459 -> p ≈ 0.05
  assert.ok(Math.abs(chiSquarePValue(3.841459, 1) - 0.05) < 1e-4);
  // qchisq(0.95, df=2) = 5.991465 -> p ≈ 0.05
  assert.ok(Math.abs(chiSquarePValue(5.991465, 2) - 0.05) < 1e-4);
  // qchisq(0.95, df=7) = 14.06714 -> p ≈ 0.05
  assert.ok(Math.abs(chiSquarePValue(14.06714, 7) - 0.05) < 1e-4);
  // 极端：chi2=0 -> p=1；巨大 chi2 -> p≈0
  assert.equal(chiSquarePValue(0, 7), 1);
  assert.ok(chiSquarePValue(10000, 7) < 1e-100);
});

test('chiSquareUniform 统计量', () => {
  const { chi2, df, expected } = chiSquareUniform([10, 10, 10, 10]);
  assert.equal(chi2, 0);
  assert.equal(df, 3);
  assert.equal(expected, 10);
  const r2 = chiSquareUniform([20, 0, 10, 10]);
  // (10^2+10^2+0+0)/10 = 20
  assert.equal(r2.chi2, 20);
});
