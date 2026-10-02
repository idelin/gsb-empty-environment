/**
 * Explicit rounding rules on exact rationals. Each rule maps a Rat to a
 * BigInt; quantize() snaps to a multiple of a rational step. Results are
 * BigInt/Rat, so a rounded zero is plain 0 (no negative zero can arise).
 */
import {
  rat, add, sub, mul, div, neg, floorRat, ceilRat, cmp,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";

export const RoundingRule = {
  Floor: "floor",
  Ceil: "ceil",
  Trunc: "trunc",
  HalfUp: "half-up", // ties towards +inf: round(x) = floor(x + 1/2)
  HalfAway: "half-away", // ties away from zero
  HalfEven: "half-even", // banker's rounding
} as const;
export type RoundingRule = (typeof RoundingRule)[keyof typeof RoundingRule];

export function roundRat(x: Rat, rule: RoundingRule): bigint {
  switch (rule) {
    case "floor":
      return floorRat(x);
    case "ceil":
      return ceilRat(x);
    case "trunc":
      return x.n / x.d; // BigInt division truncates towards zero
    case "half-up":
      return floorRat(add(x, rat(1n, 2n)));
    case "half-away":
      return x.n < 0n ? -roundRat(neg(x), "half-up") : roundRat(x, "half-up");
    case "half-even": {
      const f = floorRat(x);
      const rest = sub(x, rat(f));
      const c = cmp(rest, rat(1n, 2n));
      if (c < 0) return f;
      if (c > 0) return f + 1n;
      return f % 2n === 0n ? f : f + 1n;
    }
  }
}

/** Snap x to the nearest multiple of step under the given rule. step > 0. */
export function quantize(x: Rat, step: Rat, rule: RoundingRule): Rat {
  if (step.n <= 0n) throw new Error("quantize step must be positive");
  const k = roundRat(div(x, step), rule);
  return mul(rat(k), step);
}

/**
 * Worst-case absolute loss of one quantization:
 * half-* rules lose at most step/2, directed rules (floor/ceil/trunc) up to step.
 */
export function maxQuantizeLoss(step: Rat, rule: RoundingRule): Rat {
  switch (rule) {
    case "half-up":
    case "half-away":
    case "half-even":
      return mul(step, rat(1n, 2n));
    default:
      return step;
  }
}

export interface Plateau {
  lo: Rat;
  loClosed: boolean;
  hi: Rat;
  hiClosed: boolean;
}

/**
 * Preimage plateau of one output level: the exact set of x with
 * quantize(x, step, rule) === level * step, with correct open/closed ends.
 */
export function plateauForLevel(level: bigint, step: Rat, rule: RoundingRule): Plateau {
  const base = mul(rat(level), step);
  const half = mul(step, rat(1n, 2n));
  const lo = sub(base, half);
  const hi = add(base, half);
  switch (rule) {
    case "floor":
      return { lo: base, loClosed: true, hi: add(base, step), hiClosed: false };
    case "ceil":
      return { lo: sub(base, step), loClosed: false, hi: base, hiClosed: true };
    case "trunc":
      if (level > 0n) return { lo: base, loClosed: true, hi: add(base, step), hiClosed: false };
      if (level < 0n) return { lo: sub(base, step), loClosed: false, hi: base, hiClosed: true };
      return { lo: neg(step), loClosed: false, hi: step, hiClosed: false };
    case "half-up":
      return { lo, loClosed: true, hi, hiClosed: false };
    case "half-away":
      if (level > 0n) return { lo, loClosed: true, hi, hiClosed: false };
      if (level < 0n) return { lo, loClosed: false, hi, hiClosed: true };
      return { lo, loClosed: false, hi, hiClosed: false };
    case "half-even": {
      const even = level % 2n === 0n;
      return { lo, loClosed: even, hi, hiClosed: even };
    }
  }
}
