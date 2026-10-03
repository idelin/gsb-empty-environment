import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';

test('constructs and normalizes', () => {
  const f = new Fraction(6n, -8n);
  assert.equal(f.num, -3n);
  assert.equal(f.den, 4n);
  assert.equal(new Fraction(0n, 5n).toString(), '0');
  assert.throws(() => new Fraction(1n, 0n));
});

test('parses strings: integers, decimals, fractions, exponents', () => {
  assert.equal(Fraction.from('3/4').toString(), '3/4');
  assert.equal(Fraction.from('-0.5').toString(), '-1/2');
  assert.equal(Fraction.from('2.25').toString(), '9/4');
  assert.equal(Fraction.from('1e-3').toString(), '1/1000');
  assert.equal(Fraction.from(' 7 ').toString(), '7');
  assert.equal(Fraction.from(0.1).toString(), '1/10');
  assert.equal(Fraction.from(-2).toString(), '-2');
});

test('arithmetic is exact', () => {
  const a = Fraction.from('1/3');
  const b = Fraction.from('1/6');
  assert.equal(a.add(b).toString(), '1/2');
  assert.equal(a.sub(b).toString(), '1/6');
  assert.equal(a.mul(b).toString(), '1/18');
  assert.equal(a.div(b).toString(), '2');
  assert.equal(a.neg().toString(), '-1/3');
  // 0.1 + 0.2 !== 0.3 in floats, but exact here:
  assert.equal(Fraction.from(0.1).add(Fraction.from(0.2)).toString(), '3/10');
});

test('comparison and sign', () => {
  const a = Fraction.from('2/3');
  const b = Fraction.from('5/7');
  assert.equal(a.cmp(b), -1);
  assert.equal(b.cmp(a), 1);
  assert.equal(a.cmp(Fraction.from('4/6')), 0);
  assert.ok(a.equals(Fraction.from('4/6')));
  assert.equal(Fraction.from('-3/2').sign(), -1);
  assert.equal(Fraction.ZERO.sign(), 0);
  assert.equal(Fraction.from('-3/2').abs().toString(), '3/2');
});
