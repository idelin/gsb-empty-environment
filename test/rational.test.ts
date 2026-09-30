import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('exact big-number parsing without Number intermediates', () => {
  // 9007199254740993 is NOT representable as a double; parsing must stay exact.
  const r = Rational.fromDecimal('9007199254740993.125');
  assert.equal(r.num, 72057594037927945n);
  assert.equal(r.den, 8n);
  const sum = r.add(Rational.fromDecimal('0.875'));
  assert.equal(sum.toString(), '9007199254740994');
});

test('normalization: reduced, positive denominator, canonical zero', () => {
  assert.equal(Rational.of(2n, 4n).toString(), '1/2');
  assert.equal(Rational.of(3n, -6n).toString(), '-1/2');
  assert.equal(Rational.of(0n, 7n).toString(), '0');
  assert.throws(() => Rational.of(1n, 0n), /non-zero/);
});

test('arithmetic is exact', () => {
  const third = Rational.of(1n, 3n);
  const sixth = Rational.of(1n, 6n);
  assert.equal(third.add(sixth).toString(), '1/2');
  assert.equal(third.mul(sixth).toString(), '1/18');
  assert.equal(third.div(sixth).toString(), '2');
  assert.equal(third.sub(sixth).toString(), '1/6');
  assert.equal(third.cmp(sixth), 1);
});

test('fromInt rejects unsafe integers (no silent precision loss)', () => {
  assert.equal(Rational.fromInt(42).toString(), '42');
  assert.throws(() => Rational.fromInt(2 ** 53), /not a safe integer/);
  assert.throws(() => Rational.fromInt(1.5), /not a safe integer/);
});

test('scientific and signed decimal parsing', () => {
  assert.equal(Rational.fromDecimal('1.5e-3').toString(), '3/2000');
  assert.equal(Rational.fromDecimal('-2.5E2').toString(), '-250');
  assert.throws(() => Rational.fromDecimal('1.2.3'), /invalid decimal/);
});

test('explicit rounding rules in toDecimal', () => {
  const third = Rational.of(1n, 3n);
  const negThird = Rational.of(-1n, 3n);
  assert.equal(third.toDecimal(2, 'floor'), '0.33');
  assert.equal(third.toDecimal(2, 'ceil'), '0.34');
  assert.equal(third.toDecimal(2, 'trunc'), '0.33');
  assert.equal(negThird.toDecimal(2, 'floor'), '-0.34');
  assert.equal(negThird.toDecimal(2, 'ceil'), '-0.33');
  assert.equal(negThird.toDecimal(2, 'trunc'), '-0.33');
  // half-up: half away from zero
  assert.equal(Rational.fromDecimal('2.5').toDecimal(0, 'half-up'), '3');
  assert.equal(Rational.fromDecimal('-2.5').toDecimal(0, 'half-up'), '-3');
  // half-even: banker's rounding
  assert.equal(Rational.fromDecimal('2.5').toDecimal(0, 'half-even'), '2');
  assert.equal(Rational.fromDecimal('3.5').toDecimal(0, 'half-even'), '4');
  // exact values are untouched by every mode
  assert.equal(Rational.fromDecimal('0.125').toDecimal(3, 'half-even'), '0.125');
});
