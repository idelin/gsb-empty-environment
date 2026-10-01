/**
 * Explicit rounding modes and quantization with retained error bounds.
 *
 * `quantize` snaps an exact rational to a multiple of `step` using the
 * chosen mode, and reports both the exact rounding error and the
 * worst-case uncertainty interval [lowerBound, upperBound] that an
 * observer of only the quantized value can infer for the true value.
 */

import { Rational } from './rational.ts';

export type RoundingMode =
  | 'half-up' // ties away from zero, otherwise nearest
  | 'half-down' // ties toward zero, otherwise nearest
  | 'half-even' // ties to even multiple, otherwise nearest (banker's)
  | 'up' // away from zero
  | 'down' // toward zero (truncation)
  | 'floor' // toward negative infinity
  | 'ceil'; // toward positive infinity

export interface Quantized {
  /** Snapped value, an integer multiple of step. */
  value: Rational;
  /** Exact signed error: original - value. */
  error: Rational;
  /** Worst-case lower bound of the true value given `value` and `mode`. */
  lowerBound: Rational;
  /** Worst-case upper bound of the true value given `value` and `mode`. */
  upperBound: Rational;
}

/** Round the exact quotient num/den (den > 0) to an integer per `mode`. */
function roundQuotient(num: bigint, den: bigint, mode: RoundingMode): bigint {
  let q = num / den; // BigInt division truncates toward zero
  let r = num % den;
  if (r < 0n) {
    q -= 1n;
    r += den;
  }
  // Now q = floor(num/den) and 0 <= r < den.
  if (r === 0n) return q;
  switch (mode) {
    case 'floor':
      return q;
    case 'ceil':
      return q + 1n;
    case 'down': // toward zero
      return num < 0n ? q + 1n : q;
    case 'up': // away from zero
      return num < 0n ? q : q + 1n;
    case 'half-up':
      return 2n * r >= den ? q + 1n : q;
    case 'half-down':
      return 2n * r > den ? q + 1n : q;
    case 'half-even': {
      if (2n * r !== den) return 2n * r > den ? q + 1n : q;
      return q % 2n === 0n ? q : q + 1n;
    }
  }
}

export function quantize(
  value: Rational,
  step: Rational,
  mode: RoundingMode,
): Quantized {
  if (step.lte(Rational.ZERO)) {
    throw new RangeError('quantization step must be positive');
  }
  const ratio = value.div(step);
  const multiples = roundQuotient(ratio.num, ratio.den, mode);
  const snapped = step.mul(Rational.of(multiples));
  const error = value.sub(snapped);
  const half = step.div(Rational.of(2n));
  let lowerBound: Rational;
  let upperBound: Rational;
  switch (mode) {
    case 'half-up':
    case 'half-down':
    case 'half-even':
      lowerBound = snapped.sub(half);
      upperBound = snapped.add(half);
      break;
    case 'floor':
      lowerBound = snapped;
      upperBound = snapped.add(step);
      break;
    case 'ceil':
      lowerBound = snapped.sub(step);
      upperBound = snapped;
      break;
    case 'up': // away from zero: |true| <= |snapped|; snapped==0 implies true==0
      if (snapped.sign > 0) {
        lowerBound = snapped.sub(step);
        upperBound = snapped;
      } else if (snapped.sign < 0) {
        lowerBound = snapped;
        upperBound = snapped.add(step);
      } else {
        lowerBound = snapped;
        upperBound = snapped;
      }
      break;
    case 'down': // toward zero: |true| >= |snapped|; |true| < step when snapped==0
      if (snapped.sign > 0) {
        lowerBound = snapped;
        upperBound = snapped.add(step);
      } else if (snapped.sign < 0) {
        lowerBound = snapped.sub(step);
        upperBound = snapped;
      } else {
        lowerBound = step.neg();
        upperBound = step;
      }
      break;
  }
  return { value: snapped, error, lowerBound, upperBound };
}
