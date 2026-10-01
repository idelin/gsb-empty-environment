import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { divDecimal, sqrtDecimal } from '../src/rational.ts';

const D = Decimal.from;

test('解析与格式化：精确往返，不经 Number', () => {
  assert.equal(D('0.1').toString(), '0.1');
  assert.equal(D('-12.3400').toString(), '-12.34'); // 规范化去尾零
  assert.equal(D('1e3').toString(), '1000');
  assert.equal(D('2.5e-3').toString(), '0.0025');
  assert.equal(D('0').toString(), '0');
  assert.equal(D('-0.0').toString(), '0');
  assert.equal(D(123456789012345678901234567890n).toString(), '123456789012345678901234567890');
});

test('拒绝 number 输入：禁止大数经 Number 中转', () => {
  assert.throws(() => Decimal.from(1.5 as unknown as string), TypeError);
  assert.throws(() => Decimal.from(2 ** 53 as unknown as string), TypeError);
});

test('加减乘精确（含超出 float64 精度的大数）', () => {
  assert.equal(D('0.1').add(D('0.2')).toString(), '0.3');
  assert.equal(
    D('123456789012345678901234567890').add(D('0.000000000000000000000000000001')).toString(),
    '123456789012345678901234567890.000000000000000000000000000001',
  );
  // 独立参照：测试内直接用 BigInt 算期望乘积
  const a = 12345678901234567890n;
  const b = 98765432109876543210n;
  assert.equal(D(a.toString()).mul(D(b.toString())).toString(), (a * b).toString());
  assert.equal(D('1.5').sub(D('2.25')).toString(), '-0.75');
});

test('除法：可除尽时精确，否则按显式规则舍入', () => {
  assert.equal(divDecimal(D('10'), D('4'), 10, 'half-even').toString(), '2.5');
  assert.equal(divDecimal(D('1'), D('3'), 10, 'half-even').toString(), '0.3333333333');
  assert.equal(divDecimal(D('1'), D('6'), 10, 'half-even').toString(), '0.1666666667');
  assert.equal(divDecimal(D('2'), D('3'), 1, 'half-even').toString(), '0.7');
  assert.throws(() => divDecimal(D('1'), D('0'), 5, 'half-even'), /除数为零/);
});

test('roundToSig：half-even 恰为一半向偶数', () => {
  assert.equal(D('2.5').roundToSig(1, 'half-even').toString(), '2');
  assert.equal(D('3.5').roundToSig(1, 'half-even').toString(), '4');
  assert.equal(D('0.125').roundToSig(2, 'half-even').toString(), '0.12');
  assert.equal(D('0.135').roundToSig(2, 'half-even').toString(), '0.14');
  assert.equal(D('2.5').roundToSig(1, 'half-up').toString(), '3');
  assert.equal(D('-1.2').roundToSig(1, 'down').toString(), '-1');
  assert.equal(D('-1.2').roundToSig(1, 'up').toString(), '-2');
  assert.equal(D('-1.2').roundToSig(1, 'floor').toString(), '-2');
  assert.equal(D('-1.2').roundToSig(1, 'ceiling').toString(), '-1');
});

test('平方根：正确舍入', () => {
  assert.equal(sqrtDecimal(D('4'), 6, 'half-even').toString(), '2');
  assert.equal(sqrtDecimal(D('2'), 10, 'half-even').toString(), '1.414213562');
  assert.equal(sqrtDecimal(D('0.73'), 6, 'half-even').toString(), '0.8544');
  assert.equal(sqrtDecimal(D('0.73'), 6, 'half-even').toSigString(6), '0.854400');
  assert.equal(sqrtDecimal(D('4'), 6, 'half-even').toSigString(6), '2.00000');
  assert.equal(sqrtDecimal(D('0.01'), 6, 'half-even').toString(), '0.1');
  assert.throws(() => sqrtDecimal(D('-1'), 6, 'half-even'), /负数/);
});

test('比较与数量级', () => {
  assert.equal(D('0.2').cmp(D('0.10')), 1);
  assert.equal(D('-5').cmp(D('3')), -1);
  assert.equal(D('1e-30').cmp(D('9.99e-31')), 1);
  assert.equal(D('123').orderOfMagnitude(), 2);
  assert.equal(D('0.099').orderOfMagnitude(), -2);
});
