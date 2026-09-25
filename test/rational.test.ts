import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  Rational,
  bigintToSafeNumber,
  formatRational,
  roundToInteger,
  roundingInterval,
} from '../src/index.ts';

describe('Rational parsing', () => {
  test('parses decimals exactly', () => {
    assert.equal(Rational.parse('1.5').toString(), '3/2');
    assert.equal(Rational.parse('0.1').toString(), '1/10');
    assert.equal(Rational.parse('-273.15').toString(), '-5463/20');
    assert.equal(Rational.parse('1.5e-3').toString(), '3/2000');
    assert.equal(Rational.parse('1e+21').toString(), '1000000000000000000000');
  });

  test('parses explicit fractions and reduces them', () => {
    assert.equal(Rational.parse('22/7').toString(), '22/7');
    assert.equal(Rational.parse(' 4 / 6 ').toString(), '2/3');
  });

  test('numbers convert via their shortest round-trip decimal', () => {
    assert.equal(Rational.of(0.1).toString(), '1/10');
    assert.equal(Rational.of(1000).toString(), '1000');
  });

  test('rejects garbage', () => {
    assert.throws(() => Rational.parse('abc'));
    assert.throws(() => Rational.parse(''));
    assert.throws(() => Rational.parse('1/0'));
    assert.throws(() => Rational.of(NaN));
  });
});

describe('Rational arithmetic', () => {
  test('add/sub/mul/div are exact', () => {
    const third = Rational.of('1/3');
    assert.equal(third.add(third).toString(), '2/3');
    assert.equal(third.mul(3).toString(), '1');
    assert.equal(Rational.of('0.1').add('0.2').toString(), '3/10');
    assert.equal(Rational.of(7).div('1/2').toString(), '14');
    assert.equal(Rational.of('5/2').inv().toString(), '2/5');
  });

  test('terminating decimals and formatting', () => {
    assert.equal(Rational.of('1/8').toTerminatingDecimal(), '0.125');
    assert.equal(Rational.of('1/3').toTerminatingDecimal(), null);
    assert.equal(formatRational(Rational.of('5863/20')), '293.15');
    assert.equal(formatRational(Rational.of('1/3')), '1/3');
  });
});

describe('roundToInteger', () => {
  const r = (s: string, mode: Parameters<typeof roundToInteger>[1]) =>
    roundToInteger(Rational.of(s), mode).toString();

  test('half-up rounds ties away from zero', () => {
    assert.equal(r('7/2', 'half-up'), '4');
    assert.equal(r('-5/2', 'half-up'), '-3');
    assert.equal(r('10/3', 'half-up'), '3');
  });

  test('half-even rounds ties to the even neighbour', () => {
    assert.equal(r('5/2', 'half-even'), '2');
    assert.equal(r('7/2', 'half-even'), '4');
    assert.equal(r('-5/2', 'half-even'), '-2');
  });

  test('floor / ceil / toward-zero', () => {
    assert.equal(r('-7/2', 'floor'), '-4');
    assert.equal(r('-7/2', 'ceil'), '-3');
    assert.equal(r('-7/2', 'toward-zero'), '-3');
    assert.equal(r('7/2', 'toward-zero'), '3');
    assert.equal(r('7/2', 'floor'), '3');
    assert.equal(r('7/2', 'ceil'), '4');
  });
});

describe('roundingInterval', () => {
  test('half modes are bounded by +/- 1/2 inclusive', () => {
    const iv = roundingInterval('half-even');
    assert.equal(iv.lo.toString(), '-1/2');
    assert.equal(iv.hi.toString(), '1/2');
    assert.ok(iv.loInclusive && iv.hiInclusive);
  });

  test('floor is [0, 1)', () => {
    const iv = roundingInterval('floor');
    assert.equal(iv.lo.toString(), '0');
    assert.equal(iv.hi.toString(), '1');
    assert.ok(iv.loInclusive && !iv.hiInclusive);
  });
});

describe('bigintToSafeNumber', () => {
  test('bounds', () => {
    assert.equal(bigintToSafeNumber(1000n), 1000);
    assert.equal(bigintToSafeNumber(2n ** 53n), null);
    assert.equal(bigintToSafeNumber(2n ** 53n - 1n), Number.MAX_SAFE_INTEGER);
  });
});
