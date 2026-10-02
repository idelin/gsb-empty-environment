import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';

test('parse and normalize exact rationals', () => {
  assert.equal(Fraction.parse('3/7').toString(), '3/7');
  assert.equal(Fraction.parse('2/4').toString(), '1/2');
  assert.equal(Fraction.parse('1.25').toString(), '5/4');
  assert.equal(Fraction.parse('-0.5').toString(), '-1/2');
  assert.equal(Fraction.parse('.5').toString(), '1/2');
  assert.equal(Fraction.parse('6/-3').toString(), '-2');
  assert.equal(Fraction.parse('-2/-8').toString(), '1/4');
});

test('exact arithmetic without Number round-trip', () => {
  const a = Fraction.parse('1/3');
  const b = Fraction.parse('1/6');
  assert.equal(a.add(b).toString(), '1/2');
  assert.equal(a.sub(b).toString(), '1/6');
  assert.equal(a.mul(b).toString(), '1/18');
  assert.equal(a.div(b).toString(), '2');
});

test('values beyond 2^53 stay exact (no Number transit)', () => {
  const big = Fraction.parse('9007199254740993'); // 2^53 + 1
  assert.equal(big.toString(), '9007199254740993');
  const huge = Fraction.parse('123456789012345678901234567890/3');
  assert.equal(huge.toString(), '41152263004115226300411522630');
  const sum = big.add(new Fraction(1n, 1n));
  assert.equal(sum.toString(), '9007199254740994');
});

test('Number input is rejected explicitly', () => {
  assert.throws(() => Fraction.from(1.5 as unknown as string), /Number is forbidden/);
});

test('floor and ceil are exact for both signs', () => {
  assert.equal(Fraction.parse('7/2').floor(), 3n);
  assert.equal(Fraction.parse('7/2').ceil(), 4n);
  assert.equal(Fraction.parse('-7/2').floor(), -4n);
  assert.equal(Fraction.parse('-7/2').ceil(), -3n);
  assert.equal(Fraction.parse('-4').floor(), -4n);
});

test('explicit decimal rounding modes', () => {
  const x = Fraction.parse('1/8'); // 0.125
  assert.equal(x.toDecimal(2, 'down'), '0.12');
  assert.equal(x.toDecimal(2, 'up'), '0.13');
  assert.equal(x.toDecimal(2, 'half-up'), '0.13');
  assert.equal(x.toDecimal(2, 'half-even'), '0.12');
  const neg = Fraction.parse('-1/8');
  assert.equal(neg.toDecimal(2, 'floor'), '-0.13');
  assert.equal(neg.toDecimal(2, 'ceil'), '-0.12');
  assert.equal(neg.toDecimal(2, 'down'), '-0.12');
  assert.equal(neg.toDecimal(2, 'up'), '-0.13');
  assert.equal(Fraction.parse('5/2').toDecimal(0, 'half-even'), '2');
  assert.equal(Fraction.parse('7/2').toDecimal(0, 'half-even'), '4');
});
