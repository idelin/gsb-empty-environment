/**
 * Rounding modes. All modes are decided on the absolute value of the
 * discarded fraction, so midpoint behaviour is symmetric for +/- values
 * (except the directed modes CEILING / FLOOR, by definition).
 */
export const RoundingMode = {
  /** Round to nearest, ties to even (banker's rounding, IEEE 754 default). */
  HALF_EVEN: 'HALF_EVEN',
  /** Round to nearest, ties away from zero. */
  HALF_UP: 'HALF_UP',
  /** Round to nearest, ties toward zero. */
  HALF_DOWN: 'HALF_DOWN',
  /** Away from zero whenever anything is discarded. */
  UP: 'UP',
  /** Toward zero (truncation). */
  DOWN: 'DOWN',
  /** Toward +Infinity. */
  CEILING: 'CEILING',
  /** Toward -Infinity. */
  FLOOR: 'FLOOR',
} as const;

export type RoundingMode = (typeof RoundingMode)[keyof typeof RoundingMode];

export const ROUNDING_MODES: readonly RoundingMode[] = Object.values(RoundingMode);

export function isRoundingMode(value: unknown): value is RoundingMode {
  return typeof value === 'string' && (ROUNDING_MODES as readonly string[]).includes(value);
}

/**
 * Core rounding decision, purely on BigInt — never via Number.
 *
 * Given a non-negative truncated `quotient` and the discarded fraction
 * `remainder / denominator` (0 <= remainder < denominator), decide whether
 * the quotient magnitude must be incremented by one.
 *
 * `sign` is the sign of the overall value (+1 / -1); it only matters for the
 * directed modes CEILING and FLOOR, which keeps every other mode perfectly
 * symmetric between positive and negative inputs.
 */
export function roundIncrement(
  mode: RoundingMode,
  sign: 1 | -1,
  quotient: bigint,
  remainder: bigint,
  denominator: bigint,
): 0n | 1n {
  if (remainder === 0n) return 0n;
  switch (mode) {
    case 'DOWN':
      return 0n;
    case 'UP':
      return 1n;
    case 'CEILING':
      return sign > 0 ? 1n : 0n;
    case 'FLOOR':
      return sign < 0 ? 1n : 0n;
    case 'HALF_UP':
    case 'HALF_DOWN':
    case 'HALF_EVEN': {
      // Compare remainder with half of the denominator without Number:
      // 2*remainder ? denominator
      const twice = remainder * 2n;
      if (twice > denominator) return 1n;
      if (twice < denominator) return 0n;
      // Exact midpoint.
      if (mode === 'HALF_UP') return 1n;
      if (mode === 'HALF_DOWN') return 0n;
      return quotient % 2n === 1n ? 1n : 0n; // HALF_EVEN: tie -> make even
    }
  }
}
