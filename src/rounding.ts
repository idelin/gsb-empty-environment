/**
 * Explicit rounding rules for exact non-negative rationals.
 *
 * The supported modes are deliberately explicit; there is no "default
 * hardware rounding" anywhere in this package:
 *
 *  - "floor":    towards -infinity (== truncation, inputs are >= 0)
 *  - "ceil":     towards +infinity
 *  - "halfUp":   nearest, exact midpoints round away from zero
 *  - "halfEven": nearest, exact midpoints round to the even neighbour
 *
 * Midpoints are detected by the exact integer comparison 2*num vs den*q*2...
 * concretely `2 * remainder` against `den`; no floating point is involved.
 */
export type RoundingMode = "floor" | "ceil" | "halfUp" | "halfEven";

export const ROUNDING_MODES: readonly RoundingMode[] = [
  "floor",
  "ceil",
  "halfUp",
  "halfEven",
];

/**
 * Round the exact rational num/den (num >= 0, den > 0) to an integer
 * according to `mode`. Ties are decided by the exact test 2*(num mod den)
 * vs den.
 */
export function roundQuotient(
  num: bigint,
  den: bigint,
  mode: RoundingMode,
): bigint {
  if (den <= 0n) {
    throw new RangeError("roundQuotient: denominator must be positive");
  }
  if (num < 0n) {
    throw new RangeError("roundQuotient: negative numerator rejected");
  }
  const q = num / den;
  const r = num % den;
  if (r === 0n) return q;
  switch (mode) {
    case "floor":
      return q;
    case "ceil":
      return q + 1n;
    case "halfUp":
      return 2n * r >= den ? q + 1n : q;
    case "halfEven": {
      const twice = 2n * r;
      if (twice !== den) return twice > den ? q + 1n : q;
      // exact midpoint: choose the even neighbour
      return q % 2n === 0n ? q : q + 1n;
    }
    default:
      throw new RangeError(`roundQuotient: unknown mode ${String(mode)}`);
  }
}
