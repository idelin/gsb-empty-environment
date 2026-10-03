import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, isqrt, gcdBig } from '../src/rational.ts';

test('normalization: sign and reduction', () => {
  const r = Rational.make(6n, -8n);
  assert.equal(r.num, -3n);
  assert.equal(r.den, 4n);
  assert.equal(Rational.make(0n, 5n).den, 1n);
});

test('arithmetic is exact', () => {
  const a = Rational.of('1/2');
  const b = Rational.of('1/3');
  assert.equal(a.add(b).toString(), '5/6');
  assert.equal(a.sub(b).toString(), '1/6');
  assert.equal(a.mul(b).toString(), '1/6');
  assert.equal(a.div(b).toString(), '3/2');
  assert.equal(a.inv().toString(), '2');
  assert.equal(a.neg().toString(), '-1/2');
});

test('comparison and sign', () => {
  assert.equal(Rational.of('2/3').cmp(Rational.of('3/4')), -1);
  assert.equal(Rational.of('4/6').cmp(Rational.of('2/3')), 0);
  assert.equal(Rational.of('-1/5').sign(), -1);
  assert.equal(Rational.of(0).sign(), 0);
});

test('parsing', () => {
  assert.equal(Rational.of('-7/3').toString(), '-7/3');
  assert.equal(Rational.of(5).toString(), '5');
  assert.equal(Rational.of(5n).toString(), '5');
  assert.throws(() => Rational.of(0.5));
});

test('gcdBig and isqrt', () => {
  assert.equal(gcdBig(12n, 18n), 6n);
  assert.equal(isqrt(0n), 0n);
  assert.equal(isqrt(1n), 1n);
  assert.equal(isqrt(99n), 9n);
  assert.equal(isqrt(100n), 10n);
  assert.equal(isqrt(10n ** 40n + 7n), 10n ** 20n);
});

test('exact decimal rendering (no floats)', () => {
  assert.equal(Rational.of('1/3').toDecimal(5), '0.33333');
  assert.equal(Rational.of('-2/7').toDecimal(4), '-0.2857');
  assert.equal(Rational.of(3).toDecimal(0), '3');
});
