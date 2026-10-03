import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('normalizes to lowest terms with positive denominator', () => {
  const r = Rational.of(6n, -8n);
  assert.equal(r.num, -3n);
  assert.equal(r.den, 4n);
  assert.equal(Rational.of(0n, 5n).toString(), '0');
});

test('parses fractions and integers', () => {
  assert.equal(Rational.parse('17/4').toString(), '17/4');
  assert.equal(Rational.parse('-3').toString(), '-3');
  assert.equal(Rational.parse(' 2/6 ').toString(), '1/3');
});

test('exact arithmetic without precision loss', () => {
  const third = Rational.parse('1/3');
  const sixth = Rational.parse('1/6');
  assert.equal(third.add(sixth).toString(), '1/2');
  assert.equal(third.sub(sixth).toString(), '1/6');
  assert.equal(third.mul(sixth).toString(), '1/18');
  assert.equal(third.div(sixth).toString(), '2');
  // 1/3 * 3 must be exactly 1 (a double would risk 0.999999...)
  assert.equal(third.mul(Rational.of(3n)).toString(), '1');
});

test('comparison and sign', () => {
  assert.equal(Rational.parse('1/3').cmp(Rational.parse('2/7')), 1);
  assert.equal(Rational.parse('2/6').cmp(Rational.parse('1/3')), 0);
  assert.equal(Rational.parse('-5/2').sign(), -1);
  assert.ok(Rational.parse('4/2').eq(Rational.of(2n)));
});

test('rejects zero denominator and division by zero', () => {
  assert.throws(() => Rational.of(1n, 0n));
  assert.throws(() => Rational.one().div(Rational.zero()));
});
