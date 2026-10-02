import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { quantize } from '../src/decimal.ts';

test('parse keeps integers beyond Number.MAX_SAFE_INTEGER exact', () => {
  const r = Rational.parse('123456789012345678901234567890');
  assert.equal(r.toDecimalString(), '123456789012345678901234567890');
  // 2^53 + 1 must not collapse into 2^53 as it would through Number
  const a = Rational.parse('9007199254740993');
  const b = Rational.parse('9007199254740992');
  assert.notEqual(a.compare(b), 0);
  assert.equal(a.sub(b).toDecimalString(), '1');
});

test('parse handles fractions, signs and exponents exactly', () => {
  assert.equal(Rational.parse('-0.125').toDecimalString(), '-0.125');
  assert.equal(Rational.parse('1.5e3').toDecimalString(), '1500');
  assert.equal(Rational.parse('25e-4').toDecimalString(), '0.0025');
  assert.equal(Rational.parse('.5').toDecimalString(), '0.5');
  assert.equal(Rational.parse('0.1').add(Rational.parse('0.2')).toDecimalString(), '0.3');
  assert.throws(() => Rational.parse('abc'), /invalid decimal/);
  assert.throws(() => Rational.parse(''), /invalid decimal/);
});

test('non-terminating values render as exact fractions', () => {
  const third = Rational.parse('1').div(Rational.parse('3'));
  assert.equal(third.toDecimalString(), '1/3');
});

test('quantize half-up rounds ties away from zero', () => {
  assert.equal(quantize(Rational.parse('2.5'), 0, 'half-up').value, '3');
  assert.equal(quantize(Rational.parse('-2.5'), 0, 'half-up').value, '-3');
  assert.equal(quantize(Rational.parse('2.45'), 1, 'half-up').value, '2.5');
});

test('quantize half-even rounds ties to even', () => {
  assert.equal(quantize(Rational.parse('2.5'), 0, 'half-even').value, '2');
  assert.equal(quantize(Rational.parse('3.5'), 0, 'half-even').value, '4');
  assert.equal(quantize(Rational.parse('-2.5'), 0, 'half-even').value, '-2');
});

test('quantize half-down rounds ties toward zero', () => {
  assert.equal(quantize(Rational.parse('2.5'), 0, 'half-down').value, '2');
  assert.equal(quantize(Rational.parse('-2.5'), 0, 'half-down').value, '-2');
  assert.equal(quantize(Rational.parse('2.51'), 0, 'half-down').value, '3');
});

test('quantize directed modes', () => {
  assert.equal(quantize(Rational.parse('2.1'), 0, 'floor').value, '2');
  assert.equal(quantize(Rational.parse('-2.1'), 0, 'floor').value, '-3');
  assert.equal(quantize(Rational.parse('2.1'), 0, 'ceil').value, '3');
  assert.equal(quantize(Rational.parse('-2.1'), 0, 'ceil').value, '-2');
  assert.equal(quantize(Rational.parse('2.9'), 0, 'down').value, '2');
  assert.equal(quantize(Rational.parse('-2.9'), 0, 'down').value, '-2');
  assert.equal(quantize(Rational.parse('2.1'), 0, 'up').value, '3');
  assert.equal(quantize(Rational.parse('-2.1'), 0, 'up').value, '-3');
});

test('quantization reports exact error and a valid error bound', () => {
  const third = Rational.parse('1').div(Rational.parse('3'));
  const q = quantize(third, 2, 'half-up');
  assert.equal(q.value, '0.33');
  assert.equal(q.exact, '1/3');
  assert.equal(q.exactError, '1/300'); // exact error preserved, not rounded away
  assert.equal(q.errorBound, '0.005'); // half ulp for round-to-nearest
  // |exact - value| <= errorBound must hold
  const err = Rational.parse(q.value).sub(third).abs();
  assert.ok(err.compare(Rational.parse(q.errorBound)) <= 0);
});

test('directed rounding has a full-ulp error bound', () => {
  const q = quantize(Rational.parse('1.999'), 1, 'floor');
  assert.equal(q.value, '1.9');
  assert.equal(q.errorBound, '0.1');
  assert.equal(q.exactError, '0.099');
});

test('negative scales quantize to tens, hundreds, ...', () => {
  const q = quantize(Rational.parse('12345'), -2, 'half-up');
  assert.equal(q.value, '12300');
  assert.equal(q.errorBound, '50');
});

test('exact values quantize with zero error', () => {
  const q = quantize(Rational.parse('7.25'), 2, 'half-up');
  assert.equal(q.value, '7.25');
  assert.equal(q.exactError, '0');
});
