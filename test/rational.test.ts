import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, toRational } from '../src/rational.ts';

test('rational normalization and arithmetic', () => {
  assert.equal(Rational.of(2, 4).toString(), '1/2');
  assert.equal(Rational.of(3, -6).toString(), '-1/2');
  assert.equal(Rational.of(-3, -6).toString(), '1/2');
  const a = Rational.of(1, 3).add(Rational.of(1, 6));
  assert.equal(a.toString(), '1/2');
  const b = Rational.of(2, 3).sub(Rational.of(5, 6));
  assert.equal(b.toString(), '-1/6');
  assert.equal(Rational.of(-2, 3).cmp(Rational.of(-1, 2)), -1);
  assert.equal(Rational.of(4, 2).cmp(Rational.of(2)), 0);
});

test('rational literals convert exactly', () => {
  assert.equal(toRational(3).toString(), '3');
  assert.equal(toRational('2/3').toString(), '2/3');
  assert.equal(toRational('-7/4').toString(), '-7/4');
  assert.equal(toRational(0.1).toString(), '1/10');
  assert.equal(toRational({ num: 6, den: 8 }).toString(), '3/4');
  assert.throws(() => toRational('1/0'), RangeError);
});

test('thirds add up exactly where floats would drift', () => {
  let sum = Rational.zero();
  for (let i = 0; i < 3; i++) sum = sum.add(Rational.of(1, 3));
  assert.ok(sum.eq(Rational.of(1)));
});
