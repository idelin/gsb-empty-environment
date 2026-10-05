import { test } from 'node:test';
import assert from 'node:assert/strict';
import { frac } from '../src/fraction.ts';

test('约分与符号规范化', () => {
  assert.equal(frac(2, 4).toString(), '1/2');
  assert.equal(frac(1, -2).toString(), '-1/2');
  assert.equal(frac(-6, -9).toString(), '2/3');
  assert.equal(frac(5, 1).toString(), '5');
  assert.equal(frac(0, 7).toString(), '0');
});

test('加减乘除全部精确', () => {
  assert.equal(frac(1, 3).add(frac(1, 6)).toString(), '1/2');
  assert.equal(frac(1, 3).sub(frac(1, 2)).toString(), '-1/6');
  assert.equal(frac(2, 3).mul(frac(9, 4)).toString(), '3/2');
  assert.equal(frac(2, 3).div(frac(4, 5)).toString(), '5/6');
});

test('三分之一累加精确等于 1，不依赖浮点容忍', () => {
  const third = frac(1, 3);
  const sum = third.add(third).add(third);
  assert.ok(sum.equals(frac(1)));
  assert.equal(sum.toString(), '1');
});

test('大数运算不溢出（bigint）', () => {
  const a = frac(999999937n, 1000000007n);
  const b = frac(999999929n, 1000000009n);
  const product = a.mul(b).div(a);
  assert.ok(product.equals(b));
});

test('比较与相等', () => {
  assert.ok(frac(1, 3).compare(frac(1, 2)) < 0);
  assert.ok(frac(2, 4).equals(frac(1, 2)));
  assert.ok(frac(-1, 2).isNegative());
  assert.ok(frac(0).isZero());
});
