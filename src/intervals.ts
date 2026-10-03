import type { SchemaObject } from "./schema.ts";

/** A numeric interval with optional exclusive bounds. */
export interface Interval {
  min?: number;
  minExcl?: boolean;
  max?: number;
  maxExcl?: boolean;
}

/** Effective numeric interval from minimum/maximum/exclusive* keywords. */
export function numInterval(s: SchemaObject): Interval {
  const iv: Interval = {};
  const lows: { v: number; excl: boolean }[] = [];
  if (typeof s.minimum === "number") lows.push({ v: s.minimum, excl: s.exclusiveMinimum === true });
  if (typeof s.exclusiveMinimum === "number") lows.push({ v: s.exclusiveMinimum, excl: true });
  if (lows.length > 0) {
    let best = lows[0];
    for (const c of lows.slice(1)) {
      if (c.v > best.v || (c.v === best.v && c.excl && !best.excl)) best = c;
    }
    iv.min = best.v;
    iv.minExcl = best.excl;
  }
  const highs: { v: number; excl: boolean }[] = [];
  if (typeof s.maximum === "number") highs.push({ v: s.maximum, excl: s.exclusiveMaximum === true });
  if (typeof s.exclusiveMaximum === "number") highs.push({ v: s.exclusiveMaximum, excl: true });
  if (highs.length > 0) {
    let best = highs[0];
    for (const c of highs.slice(1)) {
      if (c.v < best.v || (c.v === best.v && c.excl && !best.excl)) best = c;
    }
    iv.max = best.v;
    iv.maxExcl = best.excl;
  }
  return iv;
}

export function lenInterval(s: SchemaObject): Interval {
  return { min: s.minLength ?? 0, max: s.maxLength };
}

export function sizeInterval(s: SchemaObject): Interval {
  return { min: s.minItems ?? 0, max: s.maxItems };
}

export function isEmptyInterval(iv: Interval): boolean {
  return (
    iv.min !== undefined &&
    iv.max !== undefined &&
    (iv.min > iv.max || (iv.min === iv.max && (iv.minExcl === true || iv.maxExcl === true)))
  );
}

export function intervalContains(iv: Interval, x: number): boolean {
  if (iv.min !== undefined && (x < iv.min || (x === iv.min && iv.minExcl === true))) return false;
  if (iv.max !== undefined && (x > iv.max || (x === iv.max && iv.maxExcl === true))) return false;
  return true;
}

/** Integer bounds [lo, hi] of an interval (may be ±Infinity). Empty iff lo > hi. */
export function intBounds(iv: Interval): { lo: number; hi: number } {
  const lo =
    iv.min === undefined ? -Infinity : iv.minExcl === true ? Math.floor(iv.min) + 1 : Math.ceil(iv.min);
  const hi =
    iv.max === undefined ? Infinity : iv.maxExcl === true ? Math.ceil(iv.max) - 1 : Math.floor(iv.max);
  return { lo, hi };
}

/**
 * Find a non-integer number inside the interval, or undefined if the
 * interval contains none (empty, or a single integer point).
 */
export function findNonInteger(iv: Interval): number | undefined {
  if (isEmptyInterval(iv)) return undefined;
  const lo = iv.min ?? -Infinity;
  const hi = iv.max ?? Infinity;
  if (lo === hi) return Number.isInteger(lo) ? undefined : lo;
  if (Number.isFinite(lo) && Number.isFinite(hi)) {
    const seen = new Set<number>();
    for (let d = 2; d <= 1 << 20; d *= 2) {
      for (let j = 1; j < d; j++) {
        const c = lo + ((hi - lo) * j) / d;
        if (seen.has(c)) continue;
        seen.add(c);
        if (!Number.isInteger(c) && intervalContains(iv, c)) return c;
      }
    }
    return undefined;
  }
  if (Number.isFinite(lo)) {
    const c = Math.ceil(lo) + 0.5;
    return intervalContains(iv, c) ? c : undefined;
  }
  if (Number.isFinite(hi)) {
    const c = Math.floor(hi) - 0.5;
    return intervalContains(iv, c) ? c : undefined;
  }
  return 0.5;
}

/** Find a non-integer accepted by interval `o` but rejected by interval `n`. */
export function findNonIntegerOutside(o: Interval, n: Interval): number | undefined {
  if (isEmptyInterval(o)) return undefined;
  if (isEmptyInterval(n)) return findNonInteger(o);
  if (n.min !== undefined) {
    const below: Interval = { min: o.min, minExcl: o.minExcl, max: n.min, maxExcl: !n.minExcl };
    const w = findNonInteger(below);
    if (w !== undefined) return w;
  }
  if (n.max !== undefined) {
    const above: Interval = { min: n.max, minExcl: !n.maxExcl, max: o.max, maxExcl: o.maxExcl };
    const w = findNonInteger(above);
    if (w !== undefined) return w;
  }
  return undefined;
}
