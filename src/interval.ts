/**
 * Intervals with explicit open/closed endpoints. A null endpoint means
 * unbounded on that side. Openness is tracked exactly so rounding
 * plateaus keep their tie-point ownership.
 */
import {
  cmp, toString as ratToString,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";

export interface Endpoint {
  v: Rat;
  closed: boolean;
}

export interface Interval {
  lo: Endpoint | null; // null = -inf
  hi: Endpoint | null; // null = +inf
}

export function interval(
  lo: Rat | null, loClosed: boolean,
  hi: Rat | null, hiClosed: boolean,
): Interval {
  const iv: Interval = {
    lo: lo === null ? null : { v: lo, closed: loClosed },
    hi: hi === null ? null : { v: hi, closed: hiClosed },
  };
  if (isEmpty(iv)) throw new Error(`empty interval: ${intervalToString(iv)}`);
  return iv;
}

export function point(v: Rat): Interval {
  return { lo: { v, closed: true }, hi: { v, closed: true } };
}

export function isEmpty(iv: Interval): boolean {
  if (iv.lo === null || iv.hi === null) return false;
  const c = cmp(iv.lo.v, iv.hi.v);
  if (c > 0) return true;
  return c === 0 && !(iv.lo.closed && iv.hi.closed);
}

export function contains(iv: Interval, x: Rat): boolean {
  if (iv.lo !== null) {
    const c = cmp(x, iv.lo.v);
    if (c < 0 || (c === 0 && !iv.lo.closed)) return false;
  }
  if (iv.hi !== null) {
    const c = cmp(x, iv.hi.v);
    if (c > 0 || (c === 0 && !iv.hi.closed)) return false;
  }
  return true;
}

/** Intersection; null when empty. */
export function intersect(a: Interval, b: Interval): Interval | null {
  let lo = a.lo;
  if (a.lo === null || (b.lo !== null && cmp(b.lo.v, a.lo.v) > 0)) lo = b.lo;
  else if (b.lo !== null && a.lo !== null && cmp(b.lo.v, a.lo.v) === 0)
    lo = { v: a.lo.v, closed: a.lo.closed && b.lo.closed };
  let hi = a.hi;
  if (a.hi === null || (b.hi !== null && cmp(b.hi.v, a.hi.v) < 0)) hi = b.hi;
  else if (b.hi !== null && a.hi !== null && cmp(b.hi.v, a.hi.v) === 0)
    hi = { v: a.hi.v, closed: a.hi.closed && b.hi.closed };
  const iv: Interval = { lo, hi };
  return isEmpty(iv) ? null : iv;
}

export function intervalToString(iv: Interval): string {
  const lo = iv.lo === null ? "-inf" : ratToString(iv.lo.v);
  const hi = iv.hi === null ? "+inf" : ratToString(iv.hi.v);
  return `${iv.lo === null || iv.lo.closed ? "[" : "("}${lo}, ${hi}${iv.hi === null || iv.hi.closed ? "]" : ")"}`;
}

/** Sort disjoint-ish intervals by lower endpoint (unbounded first). */
export function sortIntervals(list: Interval[]): Interval[] {
  return [...list].sort((a, b) => {
    if (a.lo === null) return b.lo === null ? 0 : -1;
    if (b.lo === null) return 1;
    return cmp(a.lo.v, b.lo.v);
  });
}
