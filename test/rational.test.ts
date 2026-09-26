import test from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('解析：分数、小数、科学计数、整数', () => {
  assert.strictEqual(Rational.of('1/3').toString(), '1/3');
  assert.strictEqual(Rational.of('2/4').toString(), '1/2');
  assert.strictEqual(Rational.of('-0.5').toString(), '-1/2');
  assert.strictEqual(Rational.of('1.5e2').toString(), '150');
  assert.strictEqual(Rational.of('12').toString(), '12');
  assert.strictEqual(Rational.of(0.1).toString(), '1/10');
});

test('浮点残差不丢失：0.1 + 0.2 === 0.3 精确成立', () => {
  const a = Rational.of(0.1).add(Rational.of(0.2));
  assert.ok(a.eq(Rational.of(0.3)));
  assert.notStrictEqual(0.1 + 0.2 === 0.3, true); // 对照：裸浮点不成立
});

test('三分之一累加三次精确等于一', () => {
  let acc = Rational.zero();
  for (let i = 0; i < 3; i++) acc = acc.add(Rational.of('1/3'));
  assert.ok(acc.eq(Rational.of('1')));
});

test('算术：加减乘除、符号、比较、约分', () => {
  assert.strictEqual(Rational.of('3/4').mul(Rational.of('2/3')).toString(), '1/2');
  assert.strictEqual(Rational.of('1/2').div(Rational.of('1/4')).toString(), '2');
  assert.strictEqual(Rational.of('1/2').sub(Rational.of('3/4')).toString(), '-1/4');
  assert.strictEqual(Rational.of('-2/-4').toString(), '1/2');
  assert.strictEqual(Rational.of('-1/3').sign(), -1);
  assert.strictEqual(Rational.of('2/3').cmp(Rational.of('3/4')), -1);
  assert.ok(Rational.min(Rational.of('1/3'), Rational.of('1/2')).eq(Rational.of('1/3')));
});

test('非法输入抛错', () => {
  assert.throws(() => Rational.of('1/0'));
  assert.throws(() => Rational.of('abc'));
  assert.throws(() => Rational.of(Number.NaN));
});
