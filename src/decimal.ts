/**
 * Explicit rounding of exact rationals to decimal strings.
 *
 * Rounding is a presentation-boundary concern only: every internal
 * computation stays on exact BigInt rationals, and any rounding must
 * name its mode explicitly. Implemented with BigInt division/remainder,
 * never via Number.
 */

import { HUNDRED, mul, type Rational } from './rational.ts';

export type RoundingMode =
  | 'half-up' // ties away from zero
  | 'half-even' // ties to even last kept digit (banker's rounding)
  | 'floor' // toward -infinity
  | 'ceil' // toward +infinity
  | 'toward-zero'
  | 'away-from-zero';

export function roundToDecimalString(
  value: Rational,
  scale: number,
  mode: RoundingMode,
): string {
  if (!Number.isInteger(scale) || scale < 0) {
    throw new RangeError('scale must be a non-negative integer');
  }
  const factor = 10n ** BigInt(scale);
  const scaledNum = value.num * factor;
  const quotient = scaledNum / value.den; // truncates toward zero
  const remainder = scaledNum % value.den; // sign follows scaledNum

  const negative = scaledNum < 0n;
  const absQ = quotient < 0n ? -quotient : quotient;
  const absR = remainder < 0n ? -remainder : remainder;

  let bump = false;
  if (absR !== 0n) {
    switch (mode) {
      case 'floor':
        bump = negative;
        break;
      case 'ceil':
        bump = !negative;
        break;
      case 'toward-zero':
        bump = false;
        break;
      case 'away-from-zero':
        bump = true;
        break;
      case 'half-up':
        bump = absR * 2n >= value.den;
        break;
      case 'half-even': {
        const twice = absR * 2n;
        bump = twice > value.den || (twice === value.den && absQ % 2n === 1n);
        break;
      }
    }
  }

  const rounded = absQ + (bump ? 1n : 0n);
  const digits = rounded.toString().padStart(scale + 1, '0');
  const head = scale === 0 ? digits : digits.slice(0, digits.length - scale);
  const tail = scale === 0 ? '' : `.${digits.slice(digits.length - scale)}`;
  const signPrefix = negative && rounded !== 0n ? '-' : '';
  return `${signPrefix}${head}${tail}`;
}

/** Decimal rendering with explicit scale and rounding mode. */
export function formatDecimal(
  value: Rational,
  scale: number,
  mode: RoundingMode,
): string {
  return roundToDecimalString(value, scale, mode);
}

/** Percent rendering: multiplies by 100 exactly, then rounds. Suffix "%". */
export function formatPercent(
  value: Rational,
  scale: number,
  mode: RoundingMode,
): string {
  return `${roundToDecimalString(mul(value, HUNDRED), scale, mode)}%`;
}

/** Percentage-point rendering: value is already in points. Suffix " pp". */
export function formatPp(value: Rational, scale: number, mode: RoundingMode): string {
  return `${roundToDecimalString(value, scale, mode)} pp`;
}
