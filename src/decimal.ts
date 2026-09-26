/**
 * Exact decimal numbers parsed from JSON number lexemes.
 *
 * A Decimal is stored in normalized scientific form:
 *   value = (neg ? -1 : +1) * 0.digits * 10^exp
 * where `digits` has no leading or trailing zeros ("0" iff the value is zero).
 * The original lexeme is always preserved in `raw`, so two Decimals can be
 * equal in value yet distinguishable by lexeme (e.g. "1e+02" vs "100").
 */

export interface DecimalOptions {
  /** Precision budget: max significant digits after normalization. Default 1024. */
  maxSignificantDigits?: number;
  /** Exponent budget: max absolute adjusted decimal exponent. Default 1_000_000. */
  maxAbsExponent?: number;
}

export const DEFAULT_MAX_SIGNIFICANT_DIGITS = 1024;
export const DEFAULT_MAX_ABS_EXPONENT = 1_000_000;

export class DecimalError extends Error {
  readonly lexeme: string;
  constructor(message: string, lexeme: string) {
    super(message);
    this.name = 'DecimalError';
    this.lexeme = lexeme;
  }
}

export interface Decimal {
  /** Sign. Canonical zero is non-negative, but raw "-0" keeps neg=true. */
  readonly neg: boolean;
  /** Significant digits, no leading/trailing zeros; "0" iff value is zero. */
  readonly digits: string;
  /** Adjusted exponent: value = +/- 0.digits * 10^exp. Zero has exp 0. */
  readonly exp: number;
  /** Original lexeme, exactly as it appeared in the input. */
  readonly raw: string;
}

/**
 * JSON number grammar (RFC 8259): no leading zeros, no "+", no bare ".",
 * no Infinity/NaN. This is the single source of truth for validity.
 */
const NUMBER_RE = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/;

export function isJsonNumberLexeme(raw: string): boolean {
  return NUMBER_RE.test(raw);
}

export function parseDecimal(raw: string, opts: DecimalOptions = {}): Decimal {
  const maxDigits = opts.maxSignificantDigits ?? DEFAULT_MAX_SIGNIFICANT_DIGITS;
  const maxExp = opts.maxAbsExponent ?? DEFAULT_MAX_ABS_EXPONENT;
  const m = NUMBER_RE.exec(raw);
  if (!m) {
    throw new DecimalError(`invalid JSON number lexeme: ${JSON.stringify(raw)}`, raw);
  }
  const neg = raw.charCodeAt(0) === 45; // '-'
  const intPart = m[1];
  const fracPart = m[2] === undefined ? '' : m[2].slice(1);
  const expPart = m[3] === undefined ? '' : m[3].slice(1);

  // Merged digit string represents 0.<merged> * 10^intPart.length.
  let merged = intPart + fracPart;
  let lead = 0;
  while (lead < merged.length && merged.charCodeAt(lead) === 48) lead++;
  merged = merged.slice(lead);

  // BigInt so absurd exponent literals (1e99999999999999999999) cannot
  // overflow Number before the budget check rejects them.
  const expBig = (expPart === '' ? 0n : BigInt(expPart)) + BigInt(intPart.length - lead);

  let end = merged.length;
  while (end > 0 && merged.charCodeAt(end - 1) === 48) end--;
  const digits = merged.slice(0, end);

  if (digits === '') {
    // Zero: exponent is irrelevant, even for "-0e99999999999999999999".
    return { neg, digits: '0', exp: 0, raw };
  }
  if (digits.length > maxDigits) {
    throw new DecimalError(
      `significant digit count ${digits.length} exceeds precision budget ${maxDigits}`,
      raw,
    );
  }
  const limit = BigInt(maxExp);
  if (expBig > limit || expBig < -limit) {
    throw new DecimalError(
      `adjusted decimal exponent ${expBig} exceeds exponent budget +/-${maxExp}`,
      raw,
    );
  }
  return { neg, digits, exp: Number(expBig), raw };
}

/**
 * Total order on decimal values. Compares sign, then adjusted exponent, then
 * the normalized digit strings lexicographically -- exponents are never
 * expanded, so 1e100000 vs 2e100000 is O(1) in the exponent magnitude.
 */
export function compareDecimal(a: Decimal, b: Decimal): number {
  const aZero = a.digits === '0';
  const bZero = b.digits === '0';
  if (aZero && bZero) return 0; // -0 equals +0 in value
  if (aZero) return b.neg ? 1 : -1;
  if (bZero) return a.neg ? -1 : 1;
  if (a.neg !== b.neg) return a.neg ? -1 : 1;
  const sign = a.neg ? -1 : 1;
  if (a.exp !== b.exp) return a.exp < b.exp ? -sign : sign;
  if (a.digits === b.digits) return 0;
  // No trailing zeros: a proper prefix means the shorter one pads with zeros.
  return a.digits < b.digits ? -sign : sign;
}

/** Value equality: 1e+02 equals 100 equals 100.0. */
export function decimalEquals(a: Decimal, b: Decimal): boolean {
  return compareDecimal(a, b) === 0;
}

/** Lexeme identity: "1e+02" and "100" are equal in value but not identical. */
export function decimalIdentical(a: Decimal, b: Decimal): boolean {
  return a.raw === b.raw;
}

/**
 * Canonical serialization: a unique string per mathematical value, suitable
 * as a signing key. Plain notation for adjusted exponents in [-6, 21],
 * scientific "d[.ddd]e±X" outside that range. Zero (incl. -0) maps to "0".
 * Idempotent: canonical(parse(canonical(d))) === canonical(d).
 */
export function canonicalDecimal(d: Decimal): string {
  if (d.digits === '0') return '0';
  const sign = d.neg ? '-' : '';
  const e = d.exp;
  if (e > 0 && e <= 21) {
    if (d.digits.length <= e) return sign + d.digits + '0'.repeat(e - d.digits.length);
    return sign + d.digits.slice(0, e) + '.' + d.digits.slice(e);
  }
  if (e <= 0 && e >= -6) {
    return sign + '0.' + '0'.repeat(-e) + d.digits;
  }
  const mantissa = d.digits.length === 1 ? d.digits : d.digits[0] + '.' + d.digits.slice(1);
  const sci = e - 1;
  return sign + mantissa + 'e' + (sci < 0 ? '-' : '+') + Math.abs(sci).toString(10);
}

export type SerializeMode = 'canonical' | 'asis';

/** mode "asis" replays the original lexeme; "canonical" the unique form. */
export function serializeDecimal(d: Decimal, mode: SerializeMode): string {
  return mode === 'asis' ? d.raw : canonicalDecimal(d);
}

/** Unique representation of the value, for signing / dedup keys. */
export function decimalKey(d: Decimal): string {
  return canonicalDecimal(d);
}

/** True iff the value is an integer (1e2 is; 1e-2 and 1.5 are not). */
export function isIntegerDecimal(d: Decimal): boolean {
  return d.digits === '0' || d.exp >= d.digits.length;
}

/** Exact divisibility: is `a` an integer multiple of `b`? (0.3 % 0.1 === 0) */
export function multipleOfDecimal(a: Decimal, b: Decimal): boolean {
  if (b.digits === '0') throw new DecimalError('multipleOf divisor must be non-zero', b.raw);
  if (a.digits === '0') return true;
  const coeffA = BigInt(a.digits);
  const coeffB = BigInt(b.digits);
  const scaleA = a.exp - a.digits.length;
  const scaleB = b.exp - b.digits.length;
  const s = Math.min(scaleA, scaleB);
  const num = coeffA * 10n ** BigInt(scaleA - s);
  const den = coeffB * 10n ** BigInt(scaleB - s);
  return num % den === 0n;
}
