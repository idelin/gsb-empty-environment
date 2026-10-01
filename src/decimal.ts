/**
 * Exact decimal <-> Rational conversion. Parsing accepts an optional sign,
 * integer and fraction digits, and an optional base-10 exponent. Formatting
 * produces the exact terminating decimal expansion when one exists.
 */

import { Rational } from './rational.ts';

const DECIMAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

export function parseDecimal(text: string): Rational {
  if (typeof text !== 'string') {
    throw new TypeError(`decimal input must be a string, got ${typeof text}`);
  }
  const match = DECIMAL_RE.exec(text.trim());
  if (match === null) {
    throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(text)}`);
  }
  const [, sign, intPart, fracPart, expPart] = match;
  const intDigits = intPart ?? '';
  const fracDigits = fracPart ?? '';
  if (intDigits.length === 0 && fracDigits.length === 0) {
    throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(text)}`);
  }
  const digits = (intDigits + fracDigits).replace(/^0+(?=\d)/, '');
  const coefficient = BigInt((sign === '-' ? '-' : '') + (digits === '' ? '0' : digits));
  const exponent = BigInt(expPart ?? '0') - BigInt(fracDigits.length);
  if (exponent >= 0n) {
    return Rational.of(coefficient * 10n ** exponent, 1n);
  }
  return Rational.of(coefficient, 10n ** -exponent);
}

/**
 * Exact decimal string for a terminating rational. Throws when the value
 * has no finite decimal expansion (denominator has prime factors other
 * than 2 and 5).
 */
export function decimalString(value: Rational): string {
  let den = value.den;
  let twos = 0n;
  let fives = 0n;
  while (den % 2n === 0n) {
    den /= 2n;
    twos += 1n;
  }
  while (den % 5n === 0n) {
    den /= 5n;
    fives += 1n;
  }
  if (den !== 1n) {
    throw new RangeError(
      `value ${value.toString()} has no finite decimal expansion`,
    );
  }
  const places = twos > fives ? twos : fives;
  let scaled = value.num;
  if (twos < places) scaled *= 2n ** (places - twos);
  if (fives < places) scaled *= 5n ** (places - fives);
  const negative = scaled < 0n;
  let digits = (negative ? -scaled : scaled).toString();
  const placeCount = Number(places);
  if (placeCount === 0) {
    return (negative ? '-' : '') + digits;
  }
  digits = digits.padStart(placeCount + 1, '0');
  const head = digits.slice(0, digits.length - placeCount);
  const tail = digits.slice(digits.length - placeCount);
  return `${negative ? '-' : ''}${head}.${tail}`;
}

/**
 * Human-readable exact form: a plain decimal when the expansion terminates,
 * otherwise the exact fraction "num/den".
 */
export function formatRational(value: Rational): string {
  try {
    return decimalString(value);
  } catch {
    return value.toString();
  }
}
