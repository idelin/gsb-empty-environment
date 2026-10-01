import type { Rational } from "./rational.ts";

/**
 * Rational interval. `null` bound means unbounded in that direction.
 * Unbounded is a first-class state: it is produced by measurements whose
 * error is UNKNOWN, and it must never be silently treated as 0.
 */
export interface Interval {
  readonly lo: Rational | null;
  readonly hi: Rational | null;
}

export function interval(lo: Rational | null, hi: Rational | null): Interval {
  if (lo !== null && hi !== null && lo.gt(hi)) {
    throw new Error(`empty interval: [${lo}, ${hi}]`);
  }
  return { lo, hi };
}

export function pointInterval(v: Rational): Interval {
  return { lo: v, hi: v };
}

export function unboundedInterval(): Interval {
  return { lo: null, hi: null };
}

export function isBounded(iv: Interval): boolean {
  return iv.lo !== null && iv.hi !== null;
}

export function intervalToString(iv: Interval): string {
  const lo = iv.lo === null ? "-inf" : iv.lo.toString();
  const hi = iv.hi === null ? "+inf" : iv.hi.toString();
  return `[${lo}, ${hi}]`;
}
