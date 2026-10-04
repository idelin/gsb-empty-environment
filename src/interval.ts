/**
 * Intervals over exact decimals with outward rounding.
 *
 * Three shapes are kept strictly distinct:
 *   - 'empty'   : no real number is contained
 *   - 'unknown' : every real number is contained (the whole line)
 *   - 'range'   : [lo, hi] where a null bound means unbounded (a ray)
 *
 * Every operation takes an explicit precision; lower bounds are rounded
 * toward -Infinity and upper bounds toward +Infinity, so results always
 * contain the exact mathematical range.
 */

import { Dec } from './decimal.ts';

export type Interval =
  | { readonly kind: 'empty' }
  | { readonly kind: 'unknown' }
  | { readonly kind: 'range'; readonly lo: Dec | null; readonly hi: Dec | null };

export const EMPTY: Interval = Object.freeze({ kind: 'empty' as const });
export const UNKNOWN: Interval = Object.freeze({ kind: 'unknown' as const });

export class ZeroDivisionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ZeroDivisionError';
  }
}

export function range(lo: Dec | null, hi: Dec | null): Interval {
  if (lo === null && hi === null) return UNKNOWN;
  if (lo !== null && hi !== null && Dec.cmp(lo, hi) > 0) return EMPTY;
  return { kind: 'range', lo, hi };
}

export function point(d: Dec): Interval {
  return range(d, d);
}

export function isEmpty(iv: Interval): boolean {
  return iv.kind === 'empty';
}

export function isZeroPoint(iv: Interval): boolean {
  return iv.kind === 'range' && iv.lo !== null && iv.hi !== null && Dec.isZero(iv.lo) && Dec.isZero(iv.hi);
}

export function containsValue(iv: Interval, d: Dec): boolean {
  if (iv.kind === 'empty') return false;
  if (iv.kind === 'unknown') return true;
  if (iv.lo !== null && Dec.cmp(d, iv.lo) < 0) return false;
  if (iv.hi !== null && Dec.cmp(d, iv.hi) > 0) return false;
  return true;
}

export function containsInterval(outer: Interval, inner: Interval): boolean {
  if (inner.kind === 'empty') return true;
  if (outer.kind === 'empty') return false;
  if (outer.kind === 'unknown') return true;
  if (inner.kind === 'unknown') return false;
  const loOk = inner.lo === null ? outer.lo === null : outer.lo === null || Dec.cmp(outer.lo, inner.lo) <= 0;
  const hiOk = inner.hi === null ? outer.hi === null : outer.hi === null || Dec.cmp(outer.hi, inner.hi) >= 0;
  return loOk && hiOk;
}

export function intersect(a: Interval, b: Interval): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  if (a.kind === 'unknown') return b;
  if (b.kind === 'unknown') return a;
  const lo = a.lo === null ? b.lo : b.lo === null ? a.lo : Dec.max(a.lo, b.lo);
  const hi = a.hi === null ? b.hi : b.hi === null ? a.hi : Dec.min(a.hi, b.hi);
  return range(lo, hi);
}

/** Conservative union: the smallest interval containing both operands. */
export function hull(a: Interval, b: Interval): Interval {
  if (a.kind === 'empty') return b;
  if (b.kind === 'empty') return a;
  if (a.kind === 'unknown' || b.kind === 'unknown') return UNKNOWN;
  const lo = a.lo === null || b.lo === null ? null : Dec.min(a.lo, b.lo);
  const hi = a.hi === null || b.hi === null ? null : Dec.max(a.hi, b.hi);
  return range(lo, hi);
}

export function neg(iv: Interval): Interval {
  if (iv.kind !== 'range') return iv;
  return range(iv.hi === null ? null : Dec.neg(iv.hi), iv.lo === null ? null : Dec.neg(iv.lo));
}

export function add(a: Interval, b: Interval, prec: number): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  if (a.kind === 'unknown' || b.kind === 'unknown') return UNKNOWN;
  const lo = a.lo === null || b.lo === null ? null : Dec.round(Dec.add(a.lo, b.lo), prec, 'floor');
  const hi = a.hi === null || b.hi === null ? null : Dec.round(Dec.add(a.hi, b.hi), prec, 'ceil');
  return range(lo, hi);
}

export function sub(a: Interval, b: Interval, prec: number): Interval {
  return add(a, neg(b), prec);
}

type EBound = { readonly inf: -1 | 0 | 1; readonly d: Dec | null };

function loBound(d: Dec | null): EBound {
  return d === null ? { inf: -1, d: null } : { inf: 0, d };
}

function hiBound(d: Dec | null): EBound {
  return d === null ? { inf: 1, d: null } : { inf: 0, d };
}

function ebMul(x: EBound, y: EBound): EBound {
  if (x.inf === 0 && y.inf === 0) return { inf: 0, d: Dec.mul(x.d!, y.d!) };
  const sx = x.inf !== 0 ? x.inf : Dec.sign(x.d!);
  const sy = y.inf !== 0 ? y.inf : Dec.sign(y.d!);
  const s = sx * sy;
  if (s === 0) return { inf: 0, d: Dec.zero }; // 0 * infinity -> 0
  return { inf: s > 0 ? 1 : -1, d: null };
}

function ebCmp(a: EBound, b: EBound): number {
  if (a.inf !== b.inf) return a.inf - b.inf;
  if (a.inf !== 0) return 0;
  return Dec.cmp(a.d!, b.d!);
}

export function mul(a: Interval, b: Interval, prec: number): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  if (a.kind === 'unknown' || b.kind === 'unknown') {
    const other = a.kind === 'unknown' ? b : a;
    return isZeroPoint(other) ? point(Dec.zero) : UNKNOWN;
  }
  const corners: EBound[] = [
    ebMul(loBound(a.lo), loBound(b.lo)),
    ebMul(loBound(a.lo), hiBound(b.hi)),
    ebMul(hiBound(a.hi), loBound(b.lo)),
    ebMul(hiBound(a.hi), hiBound(b.hi)),
  ];
  let mn = corners[0];
  let mx = corners[0];
  for (const c of corners) {
    if (ebCmp(c, mn) < 0) mn = c;
    if (ebCmp(c, mx) > 0) mx = c;
  }
  if (mn.inf > 0 || mx.inf < 0) throw new Error('interval mul: inconsistent bounds');
  const lo = mn.inf === 0 ? Dec.round(mn.d!, prec, 'floor') : null;
  const hi = mx.inf === 0 ? Dec.round(mx.d!, prec, 'ceil') : null;
  return range(lo, hi);
}

/** Multiply by a constant; a negative constant flips the bounds. */
export function scale(iv: Interval, k: Dec, prec: number): Interval {
  return mul(iv, point(k), prec);
}

/**
 * 1 / b. Throws ZeroDivisionError when 0 lies strictly inside b
 * (splitting across zero is the caller's policy decision, see Domain.div).
 * A zero endpoint yields an unbounded ray.
 */
export function recip(b: Interval, prec: number): Interval {
  if (b.kind === 'empty') return EMPTY;
  if (b.kind === 'unknown') throw new ZeroDivisionError('reciprocal: denominator may be zero (unknown interval)');
  const lo = b.lo;
  const hi = b.hi;
  const negSide = lo === null || Dec.sign(lo) < 0;
  const posSide = hi === null || Dec.sign(hi) > 0;
  if (negSide && posSide) throw new ZeroDivisionError('reciprocal: denominator interval strictly contains 0');
  if (!negSide && !posSide) throw new ZeroDivisionError('reciprocal: denominator is exactly zero');
  const newLo = hi === null ? Dec.zero : Dec.sign(hi) === 0 ? null : Dec.div(Dec.one, hi, prec, 'floor');
  const newHi = lo === null ? Dec.zero : Dec.sign(lo) === 0 ? null : Dec.div(Dec.one, lo, prec, 'ceil');
  return range(newLo, newHi);
}

export function div(a: Interval, b: Interval, prec: number): Interval {
  return mul(a, recip(b, prec), prec);
}

export function toString(iv: Interval): string {
  switch (iv.kind) {
    case 'empty':
      return '∅';
    case 'unknown':
      return '(-∞, ∞)';
    case 'range': {
      const l = iv.lo === null ? '-∞' : iv.lo.toString();
      const h = iv.hi === null ? '∞' : iv.hi.toString();
      return `[${l}, ${h}]`;
    }
  }
}
