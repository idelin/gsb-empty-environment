import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction, roundHalfAwayFromZero } from '../src/rational.ts';

test('fractions normalize sign and reduce', () => {
  const half = Fraction.of(2, 4);
  assert.equal(half.num, 1n);
  assert.equal(half.den, 2n);
  const neg = Fraction.of(3, -6);
  assert.equal(neg.num, -1n);
  assert.equal(neg.den, 2n);
});

test('decimal parsing is exact', () => {
  const tenth = Fraction.fromDecimal('0.1');
  assert.equal(tenth.toString(), '1/10');
  assert.equal(Fraction.fromDecimal('-3.125').toString(), '-25/8');
});

test('arithmetic stays exact where floats would drift', () => {
  let sum = Fraction.of(0);
  for (let i = 0; i < 10; i += 1) sum = sum.add(Fraction.fromDecimal('0.1'));
  assert.equal(sum.toString(), '1');
  assert.notEqual(0.1 + 0.2 === 0.3, true, 'float baseline drifts, rational does not');
});

test('snap rounds halves away from zero', () => {
  const one = Fraction.of(1);
  assert.equal(Fraction.of(7, 2).snapTo(one).toString(), '4');
  assert.equal(Fraction.of(-7, 2).snapTo(one).toString(), '-4');
  assert.equal(Fraction.of(5, 2).snapTo(one).toString(), '3');
  assert.equal(Fraction.of(13, 4).snapTo(Fraction.of(1, 2)).toString(), '7/2');
});

test('roundHalfAwayFromZero handles negatives', () => {
  assert.equal(roundHalfAwayFromZero(Fraction.of(-5, 2)).toString(), '-3');
  assert.equal(roundHalfAwayFromZero(Fraction.of(1, 3)).toString(), '0');
});
