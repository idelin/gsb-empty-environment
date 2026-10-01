import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';

test('normalizes sign and reduces to lowest terms', () => {
  const r = Rational.of(6n, -8n);
  assert.equal(r.num, -3n);
  assert.equal(r.den, 4n);
  assert.equal(Rational.of(0n, 5n).den, 1n);
});

test('rejects zero denominator', () => {
  assert.throws(() => Rational.of(1n, 0n), RangeError);
});

test('arithmetic stays exact beyond Number precision', () => {
  const a = Rational.of(9007199254740993n); // 2^53 + 1, not representable as Number
  const b = Rational.of(1n, 3n);
  const sum = a.add(b);
  assert.equal(sum.toString(), '27021597764222980/3');
  const product = Rational.of(123456789123456789n, 7n).mul(
    Rational.of(7n, 123456789123456789n),
  );
  assert.equal(product.toString(), '1');
});

test('division and comparison', () => {
  const half = Rational.of(1n, 2n);
  const third = Rational.of(1n, 3n);
  assert.equal(half.div(third).toString(), '3/2');
  assert.equal(half.cmp(third), 1);
  assert.equal(third.cmp(half), -1);
  assert.equal(third.cmp(Rational.of(2n, 6n)), 0);
  assert.ok(Rational.of(-1n, 2n).lt(Rational.ZERO));
  assert.throws(() => half.div(Rational.ZERO), RangeError);
});

test('sign, neg, abs', () => {
  assert.equal(Rational.of(-3n, 4n).sign, -1);
  assert.equal(Rational.ZERO.sign, 0);
  assert.equal(Rational.of(3n, 4n).neg().toString(), '-3/4');
  assert.equal(Rational.of(-3n, 4n).abs().toString(), '3/4');
});
