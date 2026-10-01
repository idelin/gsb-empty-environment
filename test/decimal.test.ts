import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDecimal,
  compare,
  equalsNumerically,
  toScientific,
  toFixed,
  normalize,
  quantize,
  inRange,
  assertInRange,
  isNegativeZero,
  DecimalError,
} from '../src/index.ts';

test('compare never expands huge exponents', () => {
  const a = parseDecimal('1e999999');
  const b = parseDecimal('2e999999');
  const c = parseDecimal('1e999998');
  assert.equal(compare(a, b), -1);
  assert.equal(compare(a, c), 1);
  assert.equal(compare(a, a), 0);
  assert.equal(compare(parseDecimal('-1e999999'), a), -1);
});

test('numeric equality ignores representation, not significant digits metadata', () => {
  const a = parseDecimal('1.2300');
  const b = parseDecimal('1.23');
  assert.equal(compare(a, b), 0);
  assert.ok(equalsNumerically(a, b));
  assert.equal(a.significantDigits, 5);
  assert.equal(b.significantDigits, 3);
  assert.notEqual(a.raw, b.raw);

  assert.ok(equalsNumerically(parseDecimal('1.0e3'), parseDecimal('1000')));
  assert.ok(equalsNumerically(parseDecimal('.5'), parseDecimal('0.50')));
});

test('negative zero equals zero numerically but metadata stays distinct', () => {
  const nz = parseDecimal('-0');
  const pz = parseDecimal('0');
  assert.equal(compare(nz, pz), 0);
  assert.ok(isNegativeZero(nz));
  assert.ok(!isNegativeZero(pz));
  assert.equal(toFixed(nz), '-0');
  assert.equal(toFixed(pz), '0');
  assert.equal(toFixed(parseDecimal('-0.00')), '-0.00');
});

test('scientific normalization preserves significant digits', () => {
  assert.equal(toScientific(parseDecimal('1.2300')), '1.2300e0');
  assert.equal(toScientific(parseDecimal('1.23')), '1.23e0');
  assert.equal(toScientific(parseDecimal('12300')), '1.23e4');
  assert.equal(toScientific(parseDecimal('0.00123')), '1.23e-3');
  assert.equal(toScientific(parseDecimal('-2.5e+3')), '-2.5e3');
  assert.equal(toScientific(parseDecimal('42')), '4.2e1');
  assert.equal(toScientific(parseDecimal('-0.00')), '-0.0e0');
  assert.equal(toScientific(parseDecimal('9e999999')), '9e999999');
});

test('fixed normalization and its budget', () => {
  assert.equal(toFixed(parseDecimal('1.23e3')), '1230');
  assert.equal(toFixed(parseDecimal('1.2300')), '1.2300');
  assert.equal(toFixed(parseDecimal('0.00123')), '0.00123');
  assert.equal(toFixed(parseDecimal('-2.5e+3')), '-2500');
  assert.equal(toFixed(parseDecimal('12300')), '12300');
  assert.equal(normalize(parseDecimal('1.2300'), { format: 'scientific' }), '1.2300e0');
  assert.equal(normalize(parseDecimal('1.2300'), { format: 'fixed' }), '1.2300');

  assert.throws(
    () => toFixed(parseDecimal('1e999999')),
    (err: unknown) => (err as DecimalError).code === 'BUDGET_EXCEEDED',
  );
  assert.throws(
    () => toFixed(parseDecimal('1e-200'), { maxDigits: 100 }),
    (err: unknown) => (err as DecimalError).code === 'BUDGET_EXCEEDED',
  );
});

test('quantize halfEven with exact rounding evidence', () => {
  const up = quantize(parseDecimal('1.235'), -2, 'halfEven');
  assert.equal(toFixed(up.result), '1.24');
  assert.deepEqual(
    { ...up.evidence },
    {
      mode: 'halfEven', targetExponent: -2, shift: 1, exact: false,
      discarded: '5', guardDigit: 5, sticky: false, roundedUp: true,
    },
  );

  const stays = quantize(parseDecimal('1.245'), -2, 'halfEven');
  assert.equal(toFixed(stays.result), '1.24');
  assert.equal(stays.evidence.roundedUp, false);
  assert.equal(stays.evidence.guardDigit, 5);

  const exact = quantize(parseDecimal('1.23'), -3, 'halfEven');
  assert.equal(toFixed(exact.result), '1.230');
  assert.equal(exact.evidence.exact, true);
  assert.equal(exact.evidence.discarded, '');
  assert.equal(exact.evidence.shift, -1);
});

test('all rounding modes are explicit and evidence-backed', () => {
  const q = (s: string, mode: Parameters<typeof quantize>[2]) =>
    toFixed(quantize(parseDecimal(s), -2, mode).result);

  assert.equal(q('1.239', 'down'), '1.23');
  assert.equal(q('1.231', 'up'), '1.24');
  assert.equal(q('1.235', 'halfUp'), '1.24');
  assert.equal(q('1.245', 'halfUp'), '1.25');
  assert.equal(q('1.245', 'halfDown'), '1.24');
  assert.equal(q('1.251', 'halfDown'), '1.25');
  assert.equal(q('-1.231', 'floor'), '-1.24');
  assert.equal(q('-1.231', 'ceiling'), '-1.23');
  assert.equal(q('1.239', 'floor'), '1.23');
  assert.equal(q('1.231', 'ceiling'), '1.24');

  const ev = quantize(parseDecimal('2.71828'), -3, 'up').evidence;
  assert.equal(ev.discarded, '28');
  assert.equal(ev.guardDigit, 2);
  assert.equal(ev.sticky, true);
  assert.equal(ev.roundedUp, true);
});

test('quantize keeps trailing zeros and negative zero distinct', () => {
  const tz = quantize(parseDecimal('1.2300'), -4, 'halfEven');
  assert.equal(toFixed(tz.result), '1.2300');
  assert.equal(tz.result.significantDigits, 5);
  assert.equal(tz.evidence.exact, true);

  const nz = quantize(parseDecimal('-0.001'), -2, 'down');
  assert.equal(toFixed(nz.result), '-0.00');
  assert.ok(isNegativeZero(nz.result));
  assert.equal(nz.result.significantDigits, 2);

  const pz = quantize(parseDecimal('0.001'), -2, 'down');
  assert.equal(toFixed(pz.result), '0.00');
  assert.ok(!isNegativeZero(pz.result));
});

test('quantize shift is budget-checked', () => {
  assert.throws(
    () => quantize(parseDecimal('1e-999999'), 999999),
    (err: unknown) => (err as DecimalError).code === 'BUDGET_EXCEEDED',
  );
});

test('interval constraints with huge exponents and no expansion', () => {
  assert.ok(inRange('5', { min: '1', max: '10' }));
  assert.ok(!inRange('0', { min: '1', max: '10' }));
  assert.ok(!inRange('10', { max: '10', maxExclusive: true }));
  assert.ok(!inRange('1', { min: '1', minExclusive: true }));
  assert.ok(inRange('1e999999', { min: '0', max: '2e999999' }));
  assert.ok(!inRange('3e999999', { min: '0', max: '2e999999' }));
  assert.ok(inRange('-5', { max: '0' }));
  assert.ok(inRange('123', {}));

  assert.throws(
    () => assertInRange('11', { min: '1', max: '10' }),
    (err: unknown) => (err as DecimalError).code === 'OUT_OF_RANGE',
  );
  const d = assertInRange('1.2300', { min: '1', max: '2' });
  assert.equal(d.significantDigits, 5);
});

test('big integers never pass through Number', () => {
  const d = parseDecimal('123456789012345678901234567890.0001');
  assert.equal(d.coefficient, 1234567890123456789012345678900001n);
  assert.equal(d.exponent, -4);
  const next = parseDecimal('123456789012345678901234567891');
  assert.equal(compare(parseDecimal('123456789012345678901234567890'), next), -1);
});
