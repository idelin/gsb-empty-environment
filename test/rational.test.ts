import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, roundToInteger } from '../src/rational.ts';

test('rational arithmetic is exact', () => {
  const third = Rational.of(1n, 3n);
  const sixth = Rational.of(1n, 6n);
  assert.equal(third.add(sixth).toString(), '1/2');
  assert.equal(third.mul(sixth).toString(), '1/18');
  assert.equal(third.div(sixth).toString(), '2');
  assert.equal(third.sub(sixth).toString(), '1/6');
});

test('decimal parsing is exact, not float-based', () => {
  assert.equal(Rational.parse('0.1').toString(), '1/10');
  assert.equal(Rational.parse('273.15').toString(), '5463/20');
  assert.equal(Rational.parse('-0.0254').toString(), '-127/5000');
  assert.equal(Rational.parse('1.5e3').toString(), '1500');
  assert.equal(Rational.parse('2.5e-2').toString(), '1/40');
});

test('normalization keeps sign in numerator and reduces', () => {
  assert.equal(Rational.of(6n, -8n).toString(), '-3/4');
  assert.equal(Rational.of(-6n, -8n).toString(), '3/4');
  assert.equal(Rational.of(0n, 7n).toString(), '0');
});

test('0.1 + 0.2 === 0.3 exactly (unlike float64)', () => {
  const sum = Rational.parse('0.1').add(Rational.parse('0.2'));
  assert.ok(sum.equals(Rational.parse('0.3')));
  assert.notEqual(0.1 + 0.2, 0.3);
});

test('explicit rounding modes', () => {
  const r = (s: string) => Rational.parse(s);
  assert.equal(roundToInteger(r('2.5'), 'half-even'), 2n);
  assert.equal(roundToInteger(r('3.5'), 'half-even'), 4n);
  assert.equal(roundToInteger(r('2.5'), 'half-up'), 3n);
  assert.equal(roundToInteger(r('-2.5'), 'half-up'), -2n); // ties toward +Inf
  assert.equal(roundToInteger(r('-2.5'), 'half-even'), -2n);
  assert.equal(roundToInteger(r('-1.7'), 'trunc'), -1n);
  assert.equal(roundToInteger(r('-1.7'), 'floor'), -2n);
  assert.equal(roundToInteger(r('-1.7'), 'ceil'), -1n);
  assert.equal(roundToInteger(r('1.2'), 'ceil'), 2n);
  assert.equal(roundToInteger(r('7'), 'floor'), 7n);
});
