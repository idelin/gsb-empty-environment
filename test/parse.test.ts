import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDecimal, DecimalParseError } from '../src/index.ts';

test('parses plain integers with sign and raw lexeme preserved', () => {
  const d = parseDecimal('-42');
  assert.equal(d.negative, true);
  assert.equal(d.coefficient, 42n);
  assert.equal(d.exponent, 0);
  assert.equal(d.significantDigits, 2);
  assert.equal(d.raw, '-42');

  const p = parseDecimal('+7');
  assert.equal(p.negative, false);
  assert.equal(p.coefficient, 7n);
  assert.equal(p.raw, '+7');
});

test('parses decimals and keeps trailing-zero metadata', () => {
  const a = parseDecimal('1.2300');
  assert.equal(a.coefficient, 12300n);
  assert.equal(a.exponent, -4);
  assert.equal(a.significantDigits, 5);
  assert.equal(a.raw, '1.2300');

  const b = parseDecimal('1.23');
  assert.equal(b.coefficient, 123n);
  assert.equal(b.exponent, -2);
  assert.equal(b.significantDigits, 3);
});

test('1.2300 equals 1.23 numerically but significant digits differ', () => {
  const a = parseDecimal('1.2300');
  const b = parseDecimal('1.23');
  assert.equal(a.coefficient * 10n ** BigInt(a.exponent + 4), b.coefficient * 100n);
  assert.notEqual(a.significantDigits, b.significantDigits);
});

test('integer trailing zeros without decimal point are not significant', () => {
  const d = parseDecimal('12300');
  assert.equal(d.coefficient, 12300n);
  assert.equal(d.significantDigits, 3);
});

test('leading-dot and zero forms', () => {
  const d = parseDecimal('.5');
  assert.equal(d.coefficient, 5n);
  assert.equal(d.exponent, -1);
  assert.equal(d.significantDigits, 1);

  const z = parseDecimal('0.00');
  assert.equal(z.coefficient, 0n);
  assert.equal(z.exponent, -2);
  assert.equal(z.significantDigits, 2);

  const plain = parseDecimal('0');
  assert.equal(plain.significantDigits, 1);
});

test('negative zero keeps its sign and is not confused with +0', () => {
  const nz = parseDecimal('-0');
  const pz = parseDecimal('0');
  assert.equal(nz.negative, true);
  assert.equal(nz.coefficient, 0n);
  assert.equal(pz.negative, false);
  assert.equal(nz.raw, '-0');

  const nz2 = parseDecimal('-0.00');
  assert.equal(nz2.negative, true);
  assert.equal(nz2.significantDigits, 2);
  assert.equal(nz2.exponent, -2);
});

test('scientific notation with signed exponents', () => {
  const d = parseDecimal('-2.5e+3');
  assert.equal(d.negative, true);
  assert.equal(d.coefficient, 25n);
  assert.equal(d.exponent, 2);
  assert.equal(d.raw, '-2.5e+3');

  const e = parseDecimal('1E-10');
  assert.equal(e.coefficient, 1n);
  assert.equal(e.exponent, -10);
});

test('huge exponent within budget is stored, never expanded', () => {
  const d = parseDecimal('1e999999');
  assert.equal(d.coefficient, 1n);
  assert.equal(d.exponent, 999999);
});

function assertParseError(input: string, code: string, position: number) {
  assert.throws(
    () => parseDecimal(input),
    (err: unknown) => {
      assert.ok(err instanceof DecimalParseError, `expected DecimalParseError for ${input}`);
      assert.equal(err.code, code, `code for ${JSON.stringify(input)}`);
      assert.equal(err.position, position, `position for ${JSON.stringify(input)}`);
      return true;
    },
  );
}

test('rejects illegal grouping commas with exact position', () => {
  assertParseError('1,000', 'INVALID_CHARACTER', 1);
  assertParseError('1,', 'INVALID_CHARACTER', 1);
});

test('rejects other non-strict forms', () => {
  assertParseError('1_000', 'INVALID_CHARACTER', 1);
  assertParseError(' 1', 'INVALID_CHARACTER', 0);
  assertParseError('1 ', 'INVALID_CHARACTER', 1);
  assertParseError('abc', 'INVALID_CHARACTER', 0);
  assertParseError('--1', 'INVALID_CHARACTER', 1);
  assertParseError('1..2', 'INVALID_CHARACTER', 2);
  assertParseError('1.2.3', 'INVALID_CHARACTER', 3);
  assertParseError('0x10', 'INVALID_CHARACTER', 1);
  assertParseError('NaN', 'INVALID_CHARACTER', 0);
  assertParseError('Infinity', 'INVALID_CHARACTER', 0);
  assertParseError('+', 'TRUNCATED_INPUT', 1);
  assertParseError('.', 'TRUNCATED_INPUT', 1);
  assertParseError('1.', 'TRUNCATED_INPUT', 2);
});

test('empty input', () => {
  assertParseError('', 'EMPTY_INPUT', 0);
});

test('illegal exponents carry independent positions', () => {
  assertParseError('1e', 'TRUNCATED_INPUT', 2);
  assertParseError('1e+', 'TRUNCATED_INPUT', 3);
  assertParseError('1e-', 'TRUNCATED_INPUT', 3);
  assertParseError('1ex', 'INVALID_EXPONENT', 2);
  assertParseError('1e+x', 'INVALID_EXPONENT', 3);
  assertParseError('2.5e1.5', 'INVALID_EXPONENT', 5);
  assertParseError('1e2e3', 'INVALID_EXPONENT', 3);
});

test('huge exponent hits the budget before any expansion', () => {
  assertParseError('1e1000000', 'EXPONENT_OVERFLOW', 8);
  assertParseError('1e-1000000', 'EXPONENT_OVERFLOW', 9);
  const d = parseDecimal('9e999999');
  assert.equal(d.exponent, 999999);
});

test('custom exponent budget', () => {
  assert.throws(
    () => parseDecimal('1e150', { maxExponent: 100 }),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'EXPONENT_OVERFLOW');
      assert.equal(e.position, 4);
      return true;
    },
  );
  const ok = parseDecimal('1e99', { maxExponent: 100 });
  assert.equal(ok.exponent, 99);
  assert.throws(
    () => parseDecimal('1e100', { maxExponent: 99 }),
    (err: unknown) => (err as DecimalParseError).code === 'EXPONENT_OVERFLOW',
  );
});

test('mantissa digit budget', () => {
  assert.throws(
    () => parseDecimal('123456', { maxMantissaDigits: 5 }),
    (err: unknown) => {
      const e = err as DecimalParseError;
      assert.equal(e.code, 'BUDGET_EXCEEDED');
      assert.equal(e.position, 5);
      return true;
    },
  );
});
