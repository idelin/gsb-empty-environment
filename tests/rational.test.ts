import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('构造与约分：符号归一、最大公约数约分', () => {
  assert.equal(new Rational(6n, 8n).toString(), '3/4');
  assert.equal(new Rational(6n, -8n).toString(), '-3/4');
  assert.equal(new Rational(-6n, -8n).toString(), '3/4');
  assert.equal(new Rational(0n, 5n).toString(), '0');
  assert.equal(new Rational(7n).toString(), '7');
});

test('分母为零抛出异常', () => {
  assert.throws(() => new Rational(1n, 0n), /denominator/);
});

test('from 解析：整数、分数、有限小数字符串、number', () => {
  assert.equal(Rational.from('3/4').toString(), '3/4');
  assert.equal(Rational.from('-5').toString(), '-5');
  assert.equal(Rational.from('0.1').toString(), '1/10');
  assert.equal(Rational.from('-2.75').toString(), '-11/4');
  assert.equal(Rational.from(0.1).toString(), '1/10'); // 精确十进制，非二进制浮点
  assert.equal(Rational.from(3).toString(), '3');
});

test('四则运算精确：1/3 + 1/6 = 1/2 等', () => {
  const third = new Rational(1n, 3n);
  const sixth = new Rational(1n, 6n);
  assert.ok(third.add(sixth).equals(new Rational(1n, 2n)));
  assert.ok(third.sub(sixth).equals(sixth));
  assert.ok(third.mul(sixth).equals(new Rational(1n, 18n)));
  assert.ok(third.div(sixth).equals(2n));
  assert.ok(third.neg().equals(new Rational(-1n, 3n)));
});

test('0.1 + 0.2 === 0.3（浮点做不到的事）', () => {
  const sum = Rational.from(0.1).add(Rational.from(0.2));
  assert.ok(sum.equals(Rational.from(0.3)));
  assert.notEqual(0.1 + 0.2, 0.3); // 对照：IEEE 754 失真
});

test('除以零抛出异常', () => {
  assert.throws(() => Rational.ONE.div(Rational.ZERO), /division by zero/);
});

test('比较与符号', () => {
  assert.equal(new Rational(-3n, 4n).sign(), -1);
  assert.equal(Rational.ZERO.sign(), 0);
  assert.equal(new Rational(3n, 4n).sign(), 1);
  assert.ok(new Rational(2n, 4n).equals(new Rational(1n, 2n)));
  assert.ok(new Rational(-7n, 3n).abs().equals(new Rational(7n, 3n)));
});
