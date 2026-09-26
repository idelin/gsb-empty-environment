/**
 * Exact decimal value for JSON numbers.
 *
 * A JSON number is stored as:
 *   value = (negative ? -1 : 1) * integer(digits) * 10 ** exponent
 *
 *  - `digits`   is the normalized significant-digit string: no leading or
 *               trailing zeroes; it is exactly "0" for a zero value.
 *  - `exponent` is a bigint, so integer magnitudes beyond 2**53 and decimal
 *               exponents beyond Number range are exact.
 *  - `raw`      is the original lexeme as it appeared in the JSON text, so
 *               numbers that are numerically equal but lexically different
 *               (100, 1e2, 100.0) remain distinguishable.
 *
 * No big decimal library, no floating point, no exponent expansion: magnitude
 * comparison only compares adjusted exponents and digit strings.
 */

export class DecimalError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DecimalError';
  }
}

export interface PrecisionBudget {
  /** Maximum number of mantissa digits accepted (integer + fraction digits). */
  readonly maxSignificantDigits: number;
  /** Maximum absolute value of the written decimal exponent (e.g. e±100000). */
  readonly maxExponent: bigint;
}

export const DEFAULT_BUDGET: PrecisionBudget = Object.freeze({
  maxSignificantDigits: 1000,
  maxExponent: 100000n,
});

/**
 * Strict JSON number grammar (RFC 8259):
 *   -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
 *
 * Rejects illegal leading zeroes (01, -00), truncated numbers (1., 1e),
 * and the non-finite literals Infinity / -Infinity / NaN, which are not JSON.
 */
const LEXEME_RE = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/;

export function isValidJsonNumberLexeme(text: string): boolean {
  return LEXEME_RE.test(text);
}

export class Decimal {
  readonly negative: boolean;
  readonly digits: string;
  readonly exponent: bigint;
  readonly raw: string;

  private constructor(negative: boolean, digits: string, exponent: bigint, raw: string) {
    this.negative = negative;
    this.digits = digits;
    this.exponent = exponent;
    this.raw = raw;
  }

  /**
   * Parse a complete JSON number lexeme into an exact decimal.
   * Throws DecimalError on grammar violations or precision-budget overflow.
   */
  static fromLexeme(raw: string, budget: PrecisionBudget = DEFAULT_BUDGET): Decimal {
    if (typeof raw !== 'string' || !LEXEME_RE.test(raw)) {
      throw new DecimalError(`invalid JSON number lexeme: ${JSON.stringify(raw)}`);
    }

    let mantissaText = raw;
    let exponentText = '';
    const eIndex = raw.search(/[eE]/);
    if (eIndex >= 0) {
      mantissaText = raw.slice(0, eIndex);
      exponentText = raw.slice(eIndex + 1);
    }

    let writtenExponent = 0n;
    if (exponentText !== '') {
      let exponentNegative = false;
      let digits = exponentText;
      if (digits[0] === '+') {
        digits = digits.slice(1);
      } else if (digits[0] === '-') {
        exponentNegative = true;
        digits = digits.slice(1);
      }
      // Grammar guarantees digits is [0-9]+ here. BigInt tolerates leading
      // zeroes, which are legal inside an exponent (e.g. 1e+02, 1e000001).
      writtenExponent = BigInt(digits);
      if (exponentNegative) writtenExponent = -writtenExponent;
    }

    if (writtenExponent > budget.maxExponent || writtenExponent < -budget.maxExponent) {
      throw new DecimalError(
        `number exponent ${writtenExponent} exceeds precision budget of +/-${budget.maxExponent}`,
      );
    }

    const dotIndex = mantissaText.indexOf('.');
    const integerPart = dotIndex < 0 ? mantissaText : mantissaText.slice(0, dotIndex);
    const fractionPart = dotIndex < 0 ? '' : mantissaText.slice(dotIndex + 1);

    const negative = integerPart[0] === '-';
    const integerDigits = negative ? integerPart.slice(1) : integerPart;
    const mantissaDigits = integerDigits + fractionPart;

    if (mantissaDigits.length > budget.maxSignificantDigits) {
      throw new DecimalError(
        `mantissa has ${mantissaDigits.length} digits, exceeding precision budget of ${budget.maxSignificantDigits}`,
      );
    }

    // value = intDigits.fraction * 10**writtenExponent
    //       = (mantissaDigits as integer) * 10**(writtenExponent - fracLen)
    let exponent = writtenExponent - BigInt(fractionPart.length);

    let digits = stripLeadingZeros(mantissaDigits);
    if (digits === '') {
      // Zero keeps the sign as written (-0 vs 0); numeric comparison treats
      // them as equal, canonical output drops the sign, original mode keeps it.
      return new Decimal(negative, '0', 0n, raw);
    }

    const trailingZeroCount = digits.length - digits.replace(/0+$/, '').length;
    if (trailingZeroCount > 0) {
      digits = digits.slice(0, digits.length - trailingZeroCount);
      exponent += BigInt(trailingZeroCount);
    }

    return new Decimal(negative, digits, exponent, raw);
  }

  isZero(): boolean {
    return this.digits === '0';
  }

  /** Exact integer test: 100e-1 (== 10) is an integer, 1e-1 (== 0.1) is not. */
  isInteger(): boolean {
    return this.digits === '0' || this.exponent >= 0n;
  }

  /** Numeric equality: -0 equals 0; lexically different equal numbers match. */
  equals(other: Decimal): boolean {
    return compareDecimal(this, other) === 0;
  }

  /** Canonical unique spelling; equal values have identical signatures. */
  signature(): string {
    return canonicalDecimal(this);
  }

  toString(): string {
    return canonicalDecimal(this);
  }
}

function stripLeadingZeros(text: string): string {
  let i = 0;
  while (i < text.length && text[i] === '0') i++;
  return text.slice(i);
}

/**
 * Coerce a plain JS number / bigint / numeric string into an exact Decimal.
 *
 * Plain numbers go through String(), i.e. the shortest round-trip spelling,
 * so they never inherit a binary-rounding artifact. Non-finite numbers are
 * rejected. Note: String(-0) is "0", so negative zero cannot originate from a
 * plain JS number; use a string lexeme to preserve it.
 */
export function toDecimal(
  value: Decimal | number | bigint | string,
  budget: PrecisionBudget = DEFAULT_BUDGET,
): Decimal {
  if (value instanceof Decimal) return value;
  if (typeof value === 'bigint') {
    return Decimal.fromLexeme(value.toString(), budget);
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) {
      throw new DecimalError(`non-finite number is not representable: ${String(value)}`);
    }
    return Decimal.fromLexeme(String(value), budget);
  }
  return Decimal.fromLexeme(value, budget);
}

/**
 * Compare two decimals numerically. Returns -1, 0, or 1.
 *
 * The comparison never materializes the number:
 *   1. signs decide (zero is sign-insensitive),
 *   2. adjusted exponents (exponent + digit count) decide magnitude order,
 *   3. equal adjusted exponents fall back to a lexicographic digit compare
 *      (strings at the same decimal place order exactly like their values).
 * This works for exponents far beyond Number range (e.g. 10**(10**30)).
 */
export function compareDecimal(a: Decimal, b: Decimal): number {
  const aZero = a.isZero();
  const bZero = b.isZero();
  if (aZero && bZero) return 0;
  if (aZero) return b.negative ? 1 : -1;
  if (bZero) return a.negative ? -1 : 1;

  if (a.negative !== b.negative) return a.negative ? -1 : 1;

  const magnitude = compareMagnitude(a, b);
  return a.negative && magnitude !== 0 ? -magnitude : magnitude;
}

function compareMagnitude(a: Decimal, b: Decimal): number {
  const adjustedA = a.exponent + BigInt(a.digits.length);
  const adjustedB = b.exponent + BigInt(b.digits.length);
  if (adjustedA < adjustedB) return -1;
  if (adjustedA > adjustedB) return 1;
  if (a.digits < b.digits) return -1;
  if (a.digits > b.digits) return 1;
  return 0;
}

/**
 * Canonical spelling of a decimal:
 *   - zero is always "0" (negative zero normalizes away);
 *   - plain notation for -5 < adjusted exponent <= 21;
 *   - otherwise normalized scientific notation: d[.ddd]e[+-]n,
 *     no "+" on positive exponents, no leading exponent zeroes.
 *
 * The mapping is a function of the normalized (digits, exponent) pair, so
 * canonical(canonical(x)) === canonical(x) for every valid input.
 */
export function canonicalDecimal(value: Decimal): string {
  if (value.isZero()) return '0';

  const sign = value.negative ? '-' : '';
  const adjusted = value.exponent + BigInt(value.digits.length);

  if (adjusted > 0n && adjusted <= 21n) {
    if (value.exponent >= 0n) {
      return sign + value.digits + '0'.repeat(Number(value.exponent));
    }
    const cut = Number(adjusted);
    return sign + value.digits.slice(0, cut) + '.' + value.digits.slice(cut);
  }

  if (adjusted > -6n && adjusted <= 0n) {
    return sign + '0.' + '0'.repeat(Number(-adjusted)) + value.digits;
  }

  const scientificExponent = adjusted - 1n;
  const head = value.digits[0];
  const tail = value.digits.slice(1);
  return sign + head + (tail !== '' ? '.' + tail : '') + 'e' + scientificExponent.toString();
}
