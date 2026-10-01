import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDecimal, decimalString, formatRational } from '../src/decimal.ts';
import { Rational } from '../src/rational.ts';

test('parses plain, signed, fractional and exponent forms', () => {
  assert.equal(parseDecimal('1.5').toString(), '3/2');
  assert.equal(parseDecimal('-0.001').toString(), '-1/1000');
  assert.equal(parseDecimal('1e3').toString(), '1000');
  assert.equal(parseDecimal('2.5e-2').toString(), '1/40');
  assert.equal(parseDecimal('.5').toString(), '1/2');
  assert.equal(parseDecimal('+7.').toString(), '7');
  assert.equal(parseDecimal('  42  ').toString(), '42');
});

test('parses huge decimals exactly without Number', () => {
  const r = parseDecimal('123456789012345678901234567890.0987654321');
  assert.equal(
    r.toString(),
    '1234567890123456789012345678900987654321/10000000000',
  );
  assert.equal(decimalString(r), '123456789012345678901234567890.0987654321');
});

test('rejects invalid literals', () => {
  for (const bad of ['', 'abc', '1.2.3', 'e5', '.', '-', '1e', '0x10', '1_000']) {
    assert.throws(() => parseDecimal(bad), SyntaxError, bad);
  }
});

test('formats terminating decimals exactly', () => {
  assert.equal(decimalString(parseDecimal('-12.340')), '-12.34');
  assert.equal(decimalString(Rational.of(1n, 8n)), '0.125');
  assert.equal(decimalString(Rational.of(-1n, 8n)), '-0.125');
  assert.equal(decimalString(Rational.of(5n)), '5');
});

test('decimalString rejects non-terminating values, formatRational falls back', () => {
  assert.throws(() => decimalString(Rational.of(1n, 3n)), RangeError);
  assert.equal(formatRational(Rational.of(1n, 3n)), '1/3');
  assert.equal(formatRational(Rational.of(1n, 4n)), '0.25');
});

test('round-trips through parse and format', () => {
  const samples = ['0', '-1', '3.14159', '10000000000000000000000000.0001', '-0.5'];
  for (const s of samples) {
    assert.equal(decimalString(parseDecimal(s)), s === '0' ? '0' : s);
  }
});
