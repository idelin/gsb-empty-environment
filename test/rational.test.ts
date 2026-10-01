import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Rational, RoundingMode } from '../src/rational.ts';

describe('Rational exact arithmetic (BigInt-backed)', () => {
  it('normalizes sign and gcd', () => {
    const r = Rational.of(6n, -8n);
    assert.equal(r.num, -3n);
    assert.equal(r.den, 4n);
  });

  it('cm scale is exactly 1/100', () => {
    const r = Rational.of(1n, 100n);
    assert.equal(r.toString(), '1/100');
    assert.equal(r.mul(Rational.of(37n)).toString(), '37/100');
  });

  it('parses decimal strings exactly, no binary float', () => {
    assert.equal(Rational.parse('0.1').toString(), '1/10');
    assert.equal(Rational.parse('273.15').toString(), '5463/20');
    assert.equal(Rational.parse('-0.001').toString(), '-1/1000');
    assert.equal(Rational.parse('1.25e3').toString(), '1250');
    assert.equal(Rational.parse('6.02214076e23').toString(), '602214076000000000000000');
  });

  it('handles integers far beyond Number.MAX_SAFE_INTEGER', () => {
    const big = Rational.parse('123456789012345678901234567890');
    const doubled = big.add(big);
    assert.equal(doubled.toString(), '246913578024691357802469135780');
    const third = big.div(Rational.of(3n));
    assert.equal(third.toString(), '41152263004115226300411522630');
  });

  it('rejects non-safe-integer Number input', () => {
    assert.throws(() => Rational.from(0.1), RangeError);
    assert.throws(() => Rational.from(2 ** 60), RangeError);
    assert.equal(Rational.from(42).toString(), '42');
  });

  it('rejects division by zero', () => {
    assert.throws(() => Rational.ONE.div(Rational.ZERO), RangeError);
    assert.throws(() => Rational.of(1n, 0n), RangeError);
  });
});

describe('explicit rounding rules', () => {
  const third = Rational.of(1n, 3n);
  const negThird = third.neg();

  it('requires a mode and renders each mode distinctly', () => {
    assert.equal(third.toDecimalString(2, RoundingMode.Down), '0.33');
    assert.equal(third.toDecimalString(2, RoundingMode.Up), '0.34');
    assert.equal(third.toDecimalString(2, RoundingMode.Floor), '0.33');
    assert.equal(third.toDecimalString(2, RoundingMode.Ceiling), '0.34');
    assert.equal(negThird.toDecimalString(2, RoundingMode.Down), '-0.33');
    assert.equal(negThird.toDecimalString(2, RoundingMode.Up), '-0.34');
    assert.equal(negThird.toDecimalString(2, RoundingMode.Floor), '-0.34');
    assert.equal(negThird.toDecimalString(2, RoundingMode.Ceiling), '-0.33');
  });

  it('distinguishes half-up / half-down / half-even on exact ties', () => {
    const tie = Rational.parse('2.5');
    assert.equal(tie.toDecimalString(0, RoundingMode.HalfUp), '3');
    assert.equal(tie.toDecimalString(0, RoundingMode.HalfDown), '2');
    assert.equal(tie.toDecimalString(0, RoundingMode.HalfEven), '2');
    assert.equal(Rational.parse('3.5').toDecimalString(0, RoundingMode.HalfEven), '4');
    assert.equal(Rational.parse('-2.5').toDecimalString(0, RoundingMode.HalfUp), '-3');
    assert.equal(Rational.parse('-2.5').toDecimalString(0, RoundingMode.HalfEven), '-2');
  });

  it('rounds half correctly for non-tie fractions', () => {
    assert.equal(Rational.parse('2.35').toDecimalString(1, RoundingMode.HalfUp), '2.4');
    assert.equal(Rational.parse('2.34').toDecimalString(1, RoundingMode.HalfUp), '2.3');
    assert.equal(Rational.parse('2.36').toDecimalString(1, RoundingMode.HalfDown), '2.4');
  });

  it('pads and formats zero places', () => {
    assert.equal(Rational.of(5n, 1n).toDecimalString(3, RoundingMode.Down), '5.000');
    assert.equal(Rational.of(5n, 1n).toDecimalString(0, RoundingMode.Down), '5');
    assert.equal(Rational.parse('0.0049').toDecimalString(2, RoundingMode.HalfUp), '0.00');
  });
});
