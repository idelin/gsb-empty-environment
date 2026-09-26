import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Decimal,
  DecimalError,
  canonicalDecimal,
  compareDecimal,
  isValidJsonNumberLexeme,
  toDecimal,
} from '../src/decimal.ts';

const D = (raw: string) => Decimal.fromLexeme(raw);

test('integers beyond 2**53 keep full precision', () => {
  const big = D('9007199254740993');
  assert.equal(big.digits, '9007199254740993');
  assert.equal(big.exponent, 0n);
  assert.equal(canonicalDecimal(big), '9007199254740993');
  // 9007199254740993 is not representable as a double; comparison must be exact.
  assert.equal(compareDecimal(D('9007199254740993'), D('9007199254740992')), 1);
  assert.equal(compareDecimal(D('9007199254740993'), D('9007199254740994')), -1);
  const huge = D('123456789012345678901234567890123456789');
  assert.equal(canonicalDecimal(huge), '1.23456789012345678901234567890123456789e38');
  assert.equal(compareDecimal(huge, D('123456789012345678901234567890123456790')), -1);
});

test('negative zero policy: equal to 0, sign kept in raw, dropped in canonical', () => {
  const negZero = D('-0');
  const posZero = D('0');
  assert.equal(negZero.negative, true);
  assert.equal(posZero.negative, false);
  assert.equal(compareDecimal(negZero, posZero), 0, '-0 == 0 numerically');
  assert.equal(canonicalDecimal(negZero), '0', 'canonical form normalizes -0 to 0');
  assert.equal(negZero.raw, '-0', 'raw lexeme preserves the minus sign');
  assert.equal(D('-0.0e5').raw, '-0.0e5');
  assert.equal(canonicalDecimal(D('-0.0e5')), '0');
  assert.equal(D('-0').isZero(), true);
});

test('1e+02 canonicalizes to an equivalent value and re-canonicalization is stable', () => {
  const once = canonicalDecimal(D('1e+02'));
  assert.equal(once, '100');
  assert.equal(canonicalDecimal(D(once)), once, 'canonical form is idempotent');
  assert.equal(compareDecimal(D('1e+02'), D('100')), 0);
});

test('equal values with different lexemes share a signature but stay distinguishable', () => {
  const spellings = ['100', '1e2', '1e+02', '100.0', '1000e-1', '0.1e3', '1.00E2'];
  const signature = canonicalDecimal(D('100'));
  for (const spelling of spellings) {
    const value = D(spelling);
    assert.equal(value.signature(), signature, `${spelling} shares the signature`);
    assert.equal(value.raw, spelling, 'original lexeme is preserved');
  }
  const raws = new Set(spellings.map((s) => D(s).raw));
  assert.equal(raws.size, spellings.length, 'lexemes remain distinguishable');
});

test('huge exponents compare without expansion', () => {
  const budget = { maxSignificantDigits: 1000, maxExponent: 10n ** 40n };
  const a = Decimal.fromLexeme('1e123456789012345678901234567890', budget);
  const b = Decimal.fromLexeme('2e123456789012345678901234567890', budget);
  const c = Decimal.fromLexeme('1e123456789012345678901234567891', budget);
  assert.equal(compareDecimal(a, b), -1);
  assert.equal(compareDecimal(b, a), 1);
  assert.equal(compareDecimal(a, c), -1, 'one more order of magnitude wins');
  assert.equal(compareDecimal(a, a), 0);
  assert.equal(compareDecimal(D('-1e5'), D('1e5')), -1);
  assert.equal(compareDecimal(D('-2e5'), D('-1e5')), -1, 'negative magnitudes invert');
  assert.equal(canonicalDecimal(a), '1e123456789012345678901234567890');
});

test('precision budget rejects oversized mantissas and exponents', () => {
  const budget = { maxSignificantDigits: 4, maxExponent: 10n };
  assert.throws(() => Decimal.fromLexeme('12345', budget), DecimalError);
  assert.throws(() => Decimal.fromLexeme('1.2345', budget), DecimalError);
  assert.throws(() => Decimal.fromLexeme('1e11', budget), DecimalError);
  assert.throws(() => Decimal.fromLexeme('1e-11', budget), DecimalError);
  assert.doesNotThrow(() => Decimal.fromLexeme('1234', budget));
  assert.doesNotThrow(() => Decimal.fromLexeme('1e10', budget));
  assert.doesNotThrow(() => Decimal.fromLexeme('1e-10', budget));
});

test('default budget boundaries', () => {
  assert.doesNotThrow(() => D('1e100000'));
  assert.doesNotThrow(() => D('1e-100000'));
  assert.throws(() => D('1e100001'), DecimalError);
  assert.throws(() => D('1e-100001'), DecimalError);
  assert.doesNotThrow(() => D('9'.repeat(1000)));
  assert.throws(() => D('9'.repeat(1001)), DecimalError);
});

test('grammar rejects leading zeroes, truncated forms and non-finite literals', () => {
  const invalid = [
    '', '01', '00', '-01', '-00', '0123', '1.', '.5', '+1', '1e', '1e+', '1e-',
    'Infinity', '-Infinity', 'NaN', 'nan', 'inf', '0x10', '1_0', '1 0', '--1',
    '1..2', '1ee2', 'e1', '.', '-', '0.0.0', '1e1.5',
  ];
  for (const text of invalid) {
    assert.equal(isValidJsonNumberLexeme(text), false, JSON.stringify(text));
    assert.throws(() => D(text), DecimalError, JSON.stringify(text));
  }
});

test('grammar accepts leading zeroes inside the exponent and plain zero forms', () => {
  for (const text of ['0', '-0', '0.0', '0e0', '1e+02', '1e000001', '10e-1', '0.5e1']) {
    assert.equal(isValidJsonNumberLexeme(text), true, JSON.stringify(text));
  }
  assert.equal(compareDecimal(D('1e000001'), D('10')), 0);
  assert.equal(compareDecimal(D('1e+02'), D('100')), 0);
});

test('canonical spelling table', () => {
  const cases: Array<[string, string]> = [
    ['0', '0'],
    ['-0', '0'],
    ['5', '5'],
    ['-5', '-5'],
    ['100', '100'],
    ['1e2', '100'],
    ['1e+02', '100'],
    ['12.5', '12.5'],
    ['1.25e1', '12.5'],
    ['0.001', '0.001'],
    ['1e-3', '0.001'],
    ['0.000001', '0.000001'],
    ['1e-7', '1e-7'],
    ['123456789012345678901.5', '123456789012345678901.5'],
    ['1234567890123456789012', '1.234567890123456789012e21'],
    ['-0.5', '-0.5'],
    ['9007199254740993', '9007199254740993'],
    ['1.0', '1'],
    ['10.00e0', '10'],
  ];
  for (const [input, expected] of cases) {
    assert.equal(canonicalDecimal(D(input)), expected, `canonical(${input})`);
    assert.equal(canonicalDecimal(D(expected)), expected, `canonical(${expected}) is a fixpoint`);
  }
});

test('toDecimal coerces JS values exactly and rejects non-finite numbers', () => {
  assert.equal(canonicalDecimal(toDecimal(0.1)), '0.1');
  assert.equal(canonicalDecimal(toDecimal(1e21)), '1e21');
  assert.equal(canonicalDecimal(toDecimal(9007199254740993n)), '9007199254740993');
  assert.throws(() => toDecimal(Number.NaN), DecimalError);
  assert.throws(() => toDecimal(Number.POSITIVE_INFINITY), DecimalError);
  assert.throws(() => toDecimal(Number.NEGATIVE_INFINITY), DecimalError);
});

test('isInteger is exact, not float-based', () => {
  assert.equal(D('100e-1').isInteger(), true, '100e-1 == 10');
  assert.equal(D('1e-1').isInteger(), false, '1e-1 == 0.1');
  assert.equal(D('9007199254740993').isInteger(), true);
  assert.equal(D('1.5').isInteger(), false);
  assert.equal(D('-0').isInteger(), true);
});
