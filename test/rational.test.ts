import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('十进制字符串精确解析，不经 Number 中转', () => {
  const r = Rational.of('0.1');
  assert.equal(r.num, 1n);
  assert.equal(r.den, 10n);
  // 0.1 + 0.2 必须严格等于 0.3，而不是 0.30000000000000004
  assert.equal(Rational.of('0.1').add('0.2').toString(), '3/10');
  assert.equal(Rational.of('0.1').add('0.2').toDecimalString(1), '0.3');
});

test('大整数运算不丢失精度（超出 2^53）', () => {
  const big = Rational.of(123456789012345678901234567890n);
  assert.equal(big.add(1n).toString(), '123456789012345678901234567891');
  assert.equal(big.mul(big).toString(), '15241578753238836750495351562536198787501905199875019052100');
  // 非安全整数的 number 被拒绝
  assert.throws(() => Rational.of(2 ** 53), TypeError);
  assert.throws(() => Rational.of(0.1), TypeError);
});

test('分数形式与约分', () => {
  assert.equal(Rational.of('5/9').toString(), '5/9');
  assert.equal(Rational.of('2/4').toString(), '1/2');
  assert.equal(Rational.of('-3/-6').toString(), '1/2');
  assert.equal(Rational.of('1.5e-3').toString(), '3/2000');
});

test('四则运算与比较', () => {
  assert.equal(Rational.of('1/2').add('1/3').toString(), '5/6');
  assert.equal(Rational.of('1/2').sub('1/3').toString(), '1/6');
  assert.equal(Rational.of('2/3').mul('3/4').toString(), '1/2');
  assert.equal(Rational.of('1/2').div('1/4').toString(), '2');
  assert.ok(Rational.of('1/3').compare('0.34') < 0);
  assert.throws(() => Rational.of(1n).div(0n), RangeError);
});

test('显式舍入规则：半值情形', () => {
  const twoHalf = Rational.of('2.5');
  assert.equal(twoHalf.toDecimalString(0, 'half-even'), '2');
  assert.equal(twoHalf.toDecimalString(0, 'half-up'), '3');
  assert.equal(twoHalf.toDecimalString(0, 'half-down'), '2');
  assert.equal(Rational.of('3.5').toDecimalString(0, 'half-even'), '4');
});

test('显式舍入规则：方向性舍入与负数', () => {
  const neg = Rational.of('-2.5');
  assert.equal(neg.toDecimalString(0, 'floor'), '-3');
  assert.equal(neg.toDecimalString(0, 'ceiling'), '-2');
  assert.equal(neg.toDecimalString(0, 'down'), '-2');
  assert.equal(neg.toDecimalString(0, 'up'), '-3');
  const third = Rational.of('1/3');
  assert.equal(third.toDecimalString(4, 'down'), '0.3333');
  assert.equal(third.toDecimalString(4, 'up'), '0.3334');
  assert.equal(third.toDecimalString(4, 'half-up'), '0.3333');
});

test('舍入输出无负零，非法位数抛错', () => {
  assert.equal(Rational.of('-0.001').toDecimalString(2, 'down'), '0.00');
  assert.throws(() => Rational.of(1n).toDecimalString(-1), RangeError);
});
