import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';

test('parse/toString round-trips exactly, including huge mantissas', () => {
  const cases = [
    '0', '1', '-1', '3.14', '-0.001', '123456789012345678901234567890.123456789',
    '1e5', '2.5e-3', '-7e2', '0.00000000000000000001',
  ];
  for (const c of cases) {
    assert.equal(Decimal.parse(c).toString(), Decimal.parse(c).toString());
  }
  assert.equal(Decimal.parse('1e5').toString(), '100000');
  assert.equal(Decimal.parse('2.5e-3').toString(), '0.0025');
  assert.equal(Decimal.parse('-7e2').toString(), '-700');
  assert.equal(Decimal.parse('123456789012345678901234567890.123456789').toString(),
    '123456789012345678901234567890.123456789');
});

test('exact arithmetic beyond 2^53 (no Number intermediates)', () => {
  const a = Decimal.parse('9007199254740993'); // 2^53 + 1, not representable as Number
  const one = Decimal.ONE;
  assert.equal(a.add(one).toString(), '9007199254740994');
  const big = Decimal.parse('123456789012345678901234567890');
  assert.equal(big.mul(big).toString(), '15241578753238836750495351562536198787501905199875019052100');
  assert.equal(big.sub(big).toString(), '0');
});

test('division rounding modes are explicit and exact', () => {
  const one = Decimal.ONE;
  const three = Decimal.fromInt(3);
  assert.equal(one.div(three, 4, 'floor').toString(), '0.3333');
  assert.equal(one.div(three, 4, 'ceil').toString(), '0.3334');
  assert.equal(one.div(three, 4, 'down').toString(), '0.3333');
  assert.equal(one.div(three, 4, 'up').toString(), '0.3334');
  // negative numerator: floor goes further from zero, ceil toward zero
  const neg = Decimal.ONE.neg();
  assert.equal(neg.div(three, 4, 'floor').toString(), '-0.3334');
  assert.equal(neg.div(three, 4, 'ceil').toString(), '-0.3333');
  // exact division never widens
  assert.equal(Decimal.fromInt(7).div(Decimal.fromInt(2), 4, 'up').toString(), '3.5000');
});

test('round() respects mode on both signs', () => {
  const p = Decimal.parse('1.235');
  assert.equal(p.round(2, 'floor').toString(), '1.23');
  assert.equal(p.round(2, 'ceil').toString(), '1.24');
  const n = Decimal.parse('-1.235');
  assert.equal(n.round(2, 'floor').toString(), '-1.24');
  assert.equal(n.round(2, 'ceil').toString(), '-1.23');
  // already-representable values are untouched
  assert.equal(Decimal.parse('2.5').round(4, 'up').toString(), '2.5000');
});

test('comparison and sign across different exponents', () => {
  assert.equal(Decimal.parse('0.3').cmp(Decimal.parse('0.30')), 0);
  assert.ok(Decimal.parse('1e-20').cmp(Decimal.parse('2e-20')) < 0);
  assert.ok(Decimal.parse('-5').cmp(Decimal.parse('0.0001')) < 0);
  assert.equal(Decimal.parse('-0.5').sign(), -1);
});

test('divExact2 is exact for odd mantissas', () => {
  assert.equal(Decimal.parse('0.1').divExact2().toString(), '0.05');
  assert.equal(Decimal.parse('3').divExact2().toString(), '1.5');
  assert.equal(Decimal.parse('-7').divExact2().toString(), '-3.5');
});
