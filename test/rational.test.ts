import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDecimal, makeRational, add, sub, mul, div, abs, cmp, eq,
  toExactString, toDecimalString, ZERO,
} from '../src/rational.ts';

test('parse is exact: 0.1 + 0.2 === 0.3 (unlike IEEE-754)', () => {
  const sum = add(parseDecimal('0.1'), parseDecimal('0.2'));
  assert.ok(eq(sum, parseDecimal('0.3')));
  assert.equal(toExactString(parseDecimal('0.1')), '1/10');
});

test('no Number intermediary: integers beyond 2^53 stay exact', () => {
  const big = parseDecimal('9007199254740993'); // not representable as double
  assert.equal(toExactString(big), '9007199254740993');
  const plusOne = add(big, parseDecimal('1'));
  assert.equal(toExactString(plusOne), '9007199254740994');
  // high-precision decimal
  const precise = parseDecimal('0.12345678901234567890123456789');
  assert.equal(toExactString(precise), '12345678901234567890123456789/100000000000000000000000000000');
});

test('normalization: sign to numerator, gcd reduced', () => {
  assert.deepEqual(makeRational(2n, 4n), { num: 1n, den: 2n });
  assert.deepEqual(makeRational(3n, -6n), { num: -1n, den: 2n });
  assert.deepEqual(makeRational(-4n, -8n), { num: 1n, den: 2n });
  assert.throws(() => makeRational(1n, 0n));
});

test('arithmetic and comparison', () => {
  const a = parseDecimal('-1.25');
  const b = parseDecimal('2.5');
  assert.equal(toExactString(add(a, b)), '5/4');
  assert.equal(toExactString(sub(a, b)), '-15/4');
  assert.equal(toExactString(mul(a, b)), '-25/8');
  assert.equal(toExactString(div(a, b)), '-1/2');
  assert.equal(toExactString(abs(a)), '5/4');
  assert.equal(cmp(a, b), -1);
  assert.equal(cmp(b, a), 1);
  assert.equal(cmp(a, a), 0);
  assert.ok(eq(ZERO, parseDecimal('-0.000')));
});

test('scientific notation parses exactly', () => {
  assert.equal(toExactString(parseDecimal('1.5e-3')), '3/2000');
  assert.equal(toExactString(parseDecimal('2E4')), '20000');
  assert.throws(() => parseDecimal('1e999999999'));
});

test('display rounding: half away from zero, display only', () => {
  assert.equal(toDecimalString(parseDecimal('0.125'), 2), '0.13');
  assert.equal(toDecimalString(parseDecimal('-0.125'), 2), '-0.13');
  assert.equal(toDecimalString(parseDecimal('2.5'), 0), '3');
  assert.equal(toDecimalString(parseDecimal('-2.5'), 0), '-3');
  assert.equal(toDecimalString(parseDecimal('0.124'), 2), '0.12');
  assert.equal(toDecimalString(parseDecimal('1'), 4), '1.0000');
});
