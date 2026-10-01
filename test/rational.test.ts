import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, isqrt, sqrtRational } from '../src/rational.ts';

test('parses decimals exactly, never via Number', () => {
  // 2^53 + 1 is not representable as a double; string parsing must keep it.
  const big = Rational.of('9007199254740993');
  assert.equal(big.num, 9007199254740993n);
  assert.equal(big.den, 1n);

  const tenth = Rational.of('0.1');
  assert.equal(tenth.num, 1n);
  assert.equal(tenth.den, 10n);

  assert.equal(Rational.of('1.25e-3').toString(), '1/800');
  assert.equal(Rational.of('-2.50').toString(), '-5/2');
  assert.equal(Rational.of(42n).toString(), '42');
});

test('exact arithmetic: 0.1 + 0.2 = 0.3 exactly', () => {
  const s = Rational.of('0.1').add(Rational.of('0.2'));
  assert.equal(s.cmp(Rational.of('0.3')), 0);
  assert.throws(() => Rational.of('1/3')); // strings are decimal literals only
  assert.equal(Rational.of('2').mul('3').div('4').toString(), '3/2');
  assert.equal(Rational.of('6').div('4').toString(), '3/2'); // normalized
});

test('rejects invalid input', () => {
  assert.throws(() => Rational.of('abc'));
  assert.throws(() => Rational.of('1.2.3'));
  assert.throws(() => Rational.of(Number.NaN));
  assert.throws(() => Rational.of(1n).div(0n));
});

test('explicit rounding modes on decimal rendering', () => {
  assert.equal(Rational.of('2.5').toDecimal(0, 'half-even'), '2');
  assert.equal(Rational.of('3.5').toDecimal(0, 'half-even'), '4');
  assert.equal(Rational.of('2.5').toDecimal(0, 'half-up'), '3');
  assert.equal(Rational.of('-2.5').toDecimal(0, 'half-even'), '-2');
  assert.equal(Rational.of('-2.5').toDecimal(0, 'half-up'), '-3');
  assert.equal(Rational.of('2.7').toDecimal(0, 'down'), '2');
  assert.equal(Rational.of('-2.7').toDecimal(0, 'down'), '-2');
  assert.equal(Rational.of('2.1').toDecimal(0, 'up'), '3');
  assert.equal(Rational.of('-2.1').toDecimal(0, 'up'), '-3');
  assert.equal(Rational.of('1.005').toDecimal(2, 'half-even'), '1.00');
  assert.equal(Rational.of('1.015').toDecimal(2, 'half-even'), '1.02');
});

test('isqrt is exact floor', () => {
  assert.equal(isqrt(0n), 0n);
  assert.equal(isqrt(1n), 1n);
  assert.equal(isqrt(99n), 9n);
  assert.equal(isqrt(100n), 10n);
  assert.equal(isqrt(10n ** 40n), 10n ** 20n);
  assert.equal(isqrt(10n ** 40n - 1n), 10n ** 20n - 1n);
});

test('sqrtRational: exact squares and correctly rounded irrationals', () => {
  assert.equal(sqrtRational(Rational.of(4), 6).toString(), '2');
  assert.equal(sqrtRational(Rational.of('0.25'), 6).toString(), '1/2');
  // sqrt(2) = 1.41421356237... -> 1.4142135624 at 10 places
  assert.equal(sqrtRational(Rational.of(2), 10).toDecimal(10), '1.4142135624');
  // sqrt(2/5) = 0.63245553203... -> 0.632456 at 6 places
  assert.equal(sqrtRational(Rational.of('0.4'), 6).toDecimal(6), '0.632456');
  // truncation vs away-from-zero differ for irrationals
  assert.equal(sqrtRational(Rational.of(2), 4, 'down').toDecimal(4), '1.4142');
  assert.equal(sqrtRational(Rational.of(2), 4, 'up').toDecimal(4), '1.4143');
  assert.throws(() => sqrtRational(Rational.of(-1), 6));
});

test('big values stay exact through arithmetic (no Number intermediary)', () => {
  const a = Rational.of('1000000000000000000000000000001'); // 10^30 + 1
  const b = a.mul(3);
  assert.equal(b.toString(), '3000000000000000000000000000003');
  const u = Rational.of('1e-18');
  assert.equal(u.square().mul(9).toString(), '9/1000000000000000000000000000000000000');
});
