import { DecimalError } from './errors.ts';

/**
 * Exact decimal value.
 * value = (negative ? -1 : 1) * coefficient * 10^exponent
 *
 * `coefficient` keeps every mantissa digit exactly as written (trailing zeros
 * included), so `1.2300` and `1.23` share a numeric value but differ in
 * `significantDigits` and `raw`. `negative` is preserved even for zero, so
 * negative zero is never confused with positive zero.
 */
export interface Decimal {
  readonly negative: boolean;
  readonly coefficient: bigint;
  readonly exponent: number;
  readonly significantDigits: number;
  readonly raw: string;
}

export type RoundingMode =
  | 'halfEven'
  | 'halfUp'
  | 'halfDown'
  | 'down'
  | 'up'
  | 'floor'
  | 'ceiling';

export const DEFAULT_FIXED_BUDGET = 100_000;
export const DEFAULT_QUANTIZE_SHIFT_BUDGET = 100_000;

export interface MakeDecimalParts {
  negative: boolean;
  intDigits: string;
  /** Null when no decimal point was written. */
  fracDigits: string | null;
  exponentValue: number;
  raw: string;
}

/**
 * Significant-digit rules (documented, deterministic):
 * - With a decimal point: every digit from the first non-zero digit through
 *   the last written fractional digit is significant (trailing zeros count).
 * - Without a decimal point: trailing zeros are not significant.
 * - All-zero values: fractional zero count when a point was written
 *   ("0.00" -> 2), otherwise 1 ("0" -> 1).
 */
export function makeDecimal(parts: MakeDecimalParts): Decimal {
  const { negative, intDigits, fracDigits, exponentValue, raw } = parts;
  const hasPoint = fracDigits !== null;
  const allDigits = intDigits + (fracDigits ?? '');
  const coefficient = BigInt(allDigits);
  const exponent = exponentValue - (fracDigits?.length ?? 0);

  let significantDigits: number;
  const firstNonZero = allDigits.search(/[1-9]/);
  if (firstNonZero === -1) {
    significantDigits = hasPoint ? Math.max(fracDigits.length, 1) : 1;
  } else if (hasPoint) {
    significantDigits = allDigits.length - firstNonZero;
  } else {
    const lastNonZero = allDigits.search(/[1-9]0*$/);
    significantDigits = lastNonZero - firstNonZero + 1;
  }

  return { negative, coefficient, exponent, significantDigits, raw };
}

export function isZero(d: Decimal): boolean {
  return d.coefficient === 0n;
}

export function isNegativeZero(d: Decimal): boolean {
  return d.coefficient === 0n && d.negative;
}

function digitCount(coefficient: bigint): number {
  return coefficient === 0n ? 1 : coefficient.toString().length;
}

/** Decimal exponent of the leading digit; 0 for zero values. */
export function adjustedExponent(d: Decimal): number {
  if (d.coefficient === 0n) return 0;
  return d.exponent + digitCount(d.coefficient) - 1;
}

/** The significant digit sequence, exactly `significantDigits` long. */
export function significantDigitString(d: Decimal): string {
  if (d.coefficient === 0n) return '0'.repeat(d.significantDigits);
  const s = d.coefficient.toString();
  if (s.length >= d.significantDigits) return s.slice(0, d.significantDigits);
  return s.padEnd(d.significantDigits, '0');
}

function compareMagnitude(a: Decimal, b: Decimal): number {
  const adjA = adjustedExponent(a);
  const adjB = adjustedExponent(b);
  if (adjA !== adjB) return adjA < adjB ? -1 : 1;
  const sa = a.coefficient.toString();
  const sb = b.coefficient.toString();
  const n = Math.max(sa.length, sb.length);
  for (let i = 0; i < n; i++) {
    const ca = i < sa.length ? sa.charCodeAt(i) : 48;
    const cb = i < sb.length ? sb.charCodeAt(i) : 48;
    if (ca !== cb) return ca < cb ? -1 : 1;
  }
  return 0;
}

/**
 * Numeric comparison. Never materializes 10^exponent: only signs, adjusted
 * exponents and coefficient digit strings are inspected, so values like
 * 1e999999 compare in O(digits), not O(10^999999).
 * Negative zero compares equal to positive zero (numeric equality only;
 * the `negative`/`raw` metadata still differs).
 */
export function compare(a: Decimal, b: Decimal): number {
  const aZero = a.coefficient === 0n;
  const bZero = b.coefficient === 0n;
  if (aZero && bZero) return 0;
  if (aZero) return b.negative ? 1 : -1;
  if (bZero) return a.negative ? -1 : 1;
  if (a.negative !== b.negative) return a.negative ? -1 : 1;
  const mag = compareMagnitude(a, b);
  return a.negative ? -mag : mag;
}

export function equalsNumerically(a: Decimal, b: Decimal): boolean {
  return compare(a, b) === 0;
}

/** Scientific normalization preserving the written significant digits. */
export function toScientific(d: Decimal): string {
  const sig = significantDigitString(d);
  const mantissa = sig.length > 1 ? `${sig[0]}.${sig.slice(1)}` : sig;
  const adj = d.coefficient === 0n ? 0 : adjustedExponent(d);
  return `${d.negative ? '-' : ''}${mantissa}e${adj}`;
}

export interface ToFixedOptions {
  /** Safety budget for the number of characters the expansion may take. */
  maxDigits?: number;
}

/** Fixed-point normalization. Budget-checked: huge exponents throw instead of expanding. */
export function toFixed(d: Decimal, opts: ToFixedOptions = {}): string {
  const maxDigits = opts.maxDigits ?? DEFAULT_FIXED_BUDGET;
  const sign = d.negative ? '-' : '';
  const len = digitCount(d.coefficient);
  const pointPos = len + d.exponent;

  let needed: number;
  if (d.coefficient === 0n) {
    needed = d.exponent < 0 ? 2 - d.exponent : 1;
  } else if (pointPos <= 0) {
    needed = 2 - pointPos + len;
  } else if (pointPos >= len) {
    needed = pointPos;
  } else {
    needed = len + 1;
  }
  if (needed > maxDigits) {
    throw new DecimalError(
      'BUDGET_EXCEEDED',
      `fixed-point expansion of ${d.raw} needs ${needed} digits, budget is ${maxDigits}`,
    );
  }

  if (d.coefficient === 0n) {
    return d.exponent < 0 ? `${sign}0.${'0'.repeat(-d.exponent)}` : `${sign}0`;
  }
  const s = d.coefficient.toString();
  if (pointPos <= 0) return `${sign}0.${'0'.repeat(-pointPos)}${s}`;
  if (pointPos >= len) return `${sign}${s}${'0'.repeat(pointPos - len)}`;
  return `${sign}${s.slice(0, pointPos)}.${s.slice(pointPos)}`;
}

export interface NormalizeOptions extends ToFixedOptions {
  format?: 'fixed' | 'scientific';
}

/** Normalized output, fixed or scientific. */
export function normalize(d: Decimal, opts: NormalizeOptions = {}): string {
  return (opts.format ?? 'fixed') === 'scientific' ? toScientific(d) : toFixed(d, opts);
}

export interface QuantizeEvidence {
  readonly mode: RoundingMode;
  readonly targetExponent: number;
  /** targetExponent - source exponent: positive removed digits, negative appended zeros. */
  readonly shift: number;
  /** True when nothing non-zero was discarded. */
  readonly exact: boolean;
  /** The digit string that was removed ('' when none). */
  readonly discarded: string;
  /** First discarded digit (0 when nothing was discarded). */
  readonly guardDigit: number;
  /** Any non-zero digit after the guard digit. */
  readonly sticky: boolean;
  /** Whether the kept magnitude was incremented by one quantum. */
  readonly roundedUp: boolean;
}

export interface QuantizeResult {
  readonly result: Decimal;
  readonly evidence: QuantizeEvidence;
}

export interface QuantizeOptions {
  /** Safety budget for the decimal shift (digits removed or appended). */
  maxShift?: number;
}

function pow10(n: number): bigint {
  return 10n ** BigInt(n);
}

function canonicalRaw(d: Decimal): string {
  try {
    return toFixed(d, { maxDigits: 1000 });
  } catch {
    return toScientific(d);
  }
}

function buildResult(negative: boolean, coefficient: bigint, targetExponent: number): Decimal {
  const significantDigits =
    coefficient === 0n
      ? Math.max(-targetExponent, 1)
      : coefficient.toString().length;
  const partial: Decimal = {
    negative,
    coefficient,
    exponent: targetExponent,
    significantDigits,
    raw: '',
  };
  return { ...partial, raw: canonicalRaw(partial) };
}

/**
 * Round to the quantum 10^targetExponent with an explicit rounding mode.
 * Returns the quantized value plus exact rounding evidence (discarded digits,
 * guard digit, sticky bit) so the rounding decision is fully auditable.
 * Modes are applied to the magnitude, except `floor`/`ceiling` which are
 * directional. The sign is preserved even when the result rounds to zero,
 * so quantizing -0.001 can yield negative zero -0.00.
 */
export function quantize(
  d: Decimal,
  targetExponent: number,
  mode: RoundingMode = 'halfEven',
  opts: QuantizeOptions = {},
): QuantizeResult {
  const maxShift = opts.maxShift ?? DEFAULT_QUANTIZE_SHIFT_BUDGET;
  const shift = targetExponent - d.exponent;
  if (Math.abs(shift) > maxShift) {
    throw new DecimalError(
      'BUDGET_EXCEEDED',
      `quantize shift of ${Math.abs(shift)} digits exceeds budget ${maxShift}`,
    );
  }

  if (shift <= 0) {
    const coefficient = d.coefficient * pow10(-shift);
    const evidence: QuantizeEvidence = {
      mode, targetExponent, shift, exact: true,
      discarded: '', guardDigit: 0, sticky: false, roundedUp: false,
    };
    return { result: buildResult(d.negative, coefficient, targetExponent), evidence };
  }

  const divisor = pow10(shift);
  const q = d.coefficient / divisor;
  const r = d.coefficient % divisor;
  const discarded = r === 0n
    ? '0'.repeat(shift)
    : r.toString().padStart(shift, '0');
  const guardDigit = discarded.charCodeAt(0) - 48;
  const sticky = /[1-9]/.test(discarded.slice(1));

  let roundUp: boolean;
  switch (mode) {
    case 'down': roundUp = false; break;
    case 'up': roundUp = r !== 0n; break;
    case 'halfUp': roundUp = guardDigit >= 5; break;
    case 'halfDown': roundUp = guardDigit > 5; break;
    case 'halfEven':
      roundUp = guardDigit > 5 || (guardDigit === 5 && (sticky || (q & 1n) === 1n));
      break;
    case 'floor': roundUp = d.negative && r !== 0n; break;
    case 'ceiling': roundUp = !d.negative && r !== 0n; break;
  }

  const coefficient = q + (roundUp ? 1n : 0n);
  const evidence: QuantizeEvidence = {
    mode, targetExponent, shift,
    exact: r === 0n,
    discarded, guardDigit, sticky, roundedUp: roundUp,
  };
  return { result: buildResult(d.negative, coefficient, targetExponent), evidence };
}
