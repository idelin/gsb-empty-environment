/**
 * Quantization of exact rationals to a fixed decimal scale with an explicit
 * rounding rule and a reported error bound.
 */

import { Rational, formatScaledInteger } from './rational.ts';

/**
 * Explicit rounding rules:
 * - `half-up`   : round to nearest, ties away from zero
 * - `half-down` : round to nearest, ties toward zero
 * - `half-even` : round to nearest, ties to even last digit (banker's)
 * - `floor`     : toward negative infinity
 * - `ceil`      : toward positive infinity
 * - `down`      : toward zero (truncation)
 * - `up`        : away from zero
 */
export type RoundingMode =
  | 'half-up'
  | 'half-down'
  | 'half-even'
  | 'floor'
  | 'ceil'
  | 'down'
  | 'up';

const NEAREST_MODES: ReadonlySet<RoundingMode> = new Set(['half-up', 'half-down', 'half-even']);

export interface QuantizedValue {
  /** Quantized decimal string at `scale` fraction digits. */
  value: string;
  /** Number of fraction digits the value was quantized to. */
  scale: number;
  /** Rounding rule that was applied. */
  rounding: RoundingMode;
  /** Exact pre-quantization value (decimal, or "num/den" if non-terminating). */
  exact: string;
  /** Exact |value - exact| (decimal, or "num/den" if non-terminating). */
  exactError: string;
  /** Guaranteed upper bound of |value - exact| for this scale and rule. */
  errorBound: string;
}

function floorDiv(a: bigint, b: bigint): bigint {
  // b > 0 always in our usage
  const q = a / b;
  return a % b !== 0n && a < 0n ? q - 1n : q;
}

function shouldIncrement(
  mode: RoundingMode,
  num: bigint,
  q: bigint,
  rem: bigint,
  den: bigint,
): boolean {
  if (rem === 0n) return false;
  switch (mode) {
    case 'floor':
      return false;
    case 'ceil':
      return true;
    case 'down': // toward zero
      return num < 0n;
    case 'up': // away from zero
      return num > 0n;
    case 'half-up': {
      const twice = rem * 2n;
      return twice > den || (twice === den && num >= 0n);
    }
    case 'half-down': {
      const twice = rem * 2n;
      return twice > den || (twice === den && num < 0n);
    }
    case 'half-even': {
      const twice = rem * 2n;
      return twice > den || (twice === den && q % 2n !== 0n);
    }
  }
}

/** 10^(-scale) as a plain decimal string (scale may be negative). */
export function ulpString(scale: number): string {
  if (scale < 0) return '1' + '0'.repeat(-scale);
  if (scale === 0) return '1';
  return '0.' + '0'.repeat(scale - 1) + '1';
}

/** 5 * 10^(-scale-1) as a plain decimal string (half an ulp). */
export function halfUlpString(scale: number): string {
  if (scale < 0) return '5' + '0'.repeat(-scale - 1);
  return '0.' + '0'.repeat(scale) + '5';
}

export function quantize(exact: Rational, scale: number, rounding: RoundingMode): QuantizedValue {
  if (!Number.isInteger(scale)) {
    throw new Error(`scale must be an integer, got ${scale}`);
  }
  const scaled =
    scale >= 0
      ? Rational.of(exact.num * 10n ** BigInt(scale), exact.den)
      : Rational.of(exact.num, exact.den * 10n ** BigInt(-scale));
  const q = floorDiv(scaled.num, scaled.den);
  const rem = scaled.num - q * scaled.den; // 0 <= rem < den
  const rounded = shouldIncrement(rounding, scaled.num, q, rem, scaled.den) ? q + 1n : q;

  const quantized =
    scale >= 0
      ? Rational.of(rounded, 10n ** BigInt(scale))
      : Rational.of(rounded * 10n ** BigInt(-scale));
  const exactError = quantized.sub(exact).abs();

  return {
    value: formatScaledInteger(rounded, scale),
    scale,
    rounding,
    exact: exact.toDecimalString(),
    exactError: exactError.toDecimalString(),
    errorBound: NEAREST_MODES.has(rounding) ? halfUlpString(scale) : ulpString(scale),
  };
}
