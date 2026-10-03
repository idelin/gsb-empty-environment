/**
 * Explicit rounding rules on exact rationals. Every rule is a named mode
 * plus a decimal place count; the semantics of each mode are fixed here
 * and nowhere implicit.
 */
import { Rational, floorDiv } from "./rational.ts";

export type RoundingMode =
  | "half-up" // ties go toward +Infinity  (-2.5 -> -2,  2.5 -> 3)
  | "half-down" // ties go toward -Infinity  (-2.5 -> -3,  2.5 -> 2)
  | "half-even" // ties go to the even neighbour (banker's)
  | "toward-zero" // truncate
  | "away-from-zero"
  | "floor" // toward -Infinity
  | "ceil"; // toward +Infinity

export const ROUNDING_MODES: RoundingMode[] = [
  "half-up",
  "half-down",
  "half-even",
  "toward-zero",
  "away-from-zero",
  "floor",
  "ceil",
];

export interface RoundingRule {
  /** Decimal places to keep; may be negative (e.g. -2 rounds to hundreds). */
  places: number;
  mode: RoundingMode;
}

export function roundingRuleKey(rule: RoundingRule): string {
  return `round(places=${rule.places},mode=${rule.mode})`;
}

/** Round an exact rational to an integer under the given mode. */
export function roundToInteger(r: Rational, mode: RoundingMode): bigint {
  const floor = floorDiv(r.num, r.den);
  const rem = r.num - floor * r.den; // 0 <= rem < den
  if (rem === 0n) return floor;
  const ceil = floor + 1n;
  switch (mode) {
    case "floor":
      return floor;
    case "ceil":
      return ceil;
    case "toward-zero":
      return r.num >= 0n ? floor : ceil;
    case "away-from-zero":
      return r.num >= 0n ? ceil : floor;
    case "half-up":
    case "half-down":
    case "half-even": {
      const twice = 2n * rem;
      if (twice < r.den) return floor;
      if (twice > r.den) return ceil;
      // exact tie
      if (mode === "half-up") return ceil;
      if (mode === "half-down") return floor;
      return floor % 2n === 0n ? floor : ceil;
    }
  }
}

/** Round to `rule.places` decimal places, exactly. */
export function roundRational(r: Rational, rule: RoundingRule): Rational {
  const quantum =
    rule.places >= 0
      ? Rational.of(1n, 10n ** BigInt(rule.places))
      : Rational.of(10n ** BigInt(-rule.places), 1n);
  const scaled = r.div(quantum);
  const roundedInt = roundToInteger(scaled, rule.mode);
  return Rational.of(roundedInt, 1n).mul(quantum);
}
