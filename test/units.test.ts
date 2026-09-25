import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { convertRational, getUnit, checkCompatible } from '../src/units.ts';

const u = (name: string) => {
  const unit = getUnit(name);
  assert.ok(unit, `unit ${name} registered`);
  return unit;
};

test('1 metre is exactly 1000 millimetres', () => {
  const out = convertRational(Rational.one(), u('m'), u('mm'));
  assert.ok(out.equals(Rational.of(1000n)));
  assert.equal(out.toString(), '1000');
});

test('absolute temperature uses the affine offset', () => {
  const out = convertRational(Rational.of(20n), u('degC'), u('K'));
  assert.equal(out.toString(), '5863/20'); // 293.15
  assert.equal(out.toDecimalString(), '293.15');
});

test('temperature difference ignores the affine offset', () => {
  const out = convertRational(Rational.of(20n), u('dC'), u('dK'));
  assert.equal(out.toString(), '20'); // NOT 293.15
});

test('absolute Fahrenheit round-trips through kelvin exactly', () => {
  const f = convertRational(Rational.of(32n), u('degF'), u('K'));
  assert.equal(f.toString(), '5463/20'); // 273.15
  const back = convertRational(f, u('K'), u('degF'));
  assert.equal(back.toString(), '32');
});

test('Fahrenheit difference: 18 dF is exactly 10 dC', () => {
  const out = convertRational(Rational.of(18n), u('dF'), u('dC'));
  assert.equal(out.toString(), '10');
});

test('absolute vs difference kinds are incompatible', () => {
  assert.match(checkCompatible(u('degC'), u('dK')) ?? '', /kind mismatch/);
  assert.match(checkCompatible(u('dC'), u('K')) ?? '', /kind mismatch/);
  assert.equal(checkCompatible(u('m'), u('mm')), null);
});

test('dimensions are incompatible across quantities', () => {
  assert.match(checkCompatible(u('m'), u('kg')) ?? '', /dimension mismatch/);
});
