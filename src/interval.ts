/**
 * Intervals with exact Decimal bounds. `null` bounds are infinities, so the
 * whole real line (UNKNOWN) is representable and stays distinct from EMPTY.
 * Every arithmetic result is rounded outward to `scale` fractional digits:
 * lower bounds with 'floor', upper bounds with 'ceil'.
 */

import { Decimal } from './decimal.ts';

export type Bound = Decimal | null; // null = -inf (as lo) or +inf (as hi)

export type Interval =
  | { readonly kind: 'empty' }
  | { readonly kind: 'bounded'; readonly lo: Bound; readonly hi: Bound };

export const EMPTY: Interval = { kind: 'empty' };
export const UNKNOWN: Interval = { kind: 'bounded', lo: null, hi: null };

/** Certificate recording one interval arithmetic step for later checking. */
export interface IntervalOpCert {
  readonly kind: 'interval-op';
  readonly op: 'add' | 'sub' | 'mul' | 'div';
  readonly lhs: Interval;
  readonly rhs: Interval;
  readonly scale: number;
  readonly segments: readonly Interval[]; // >1 only when division splits
}

export class ZeroCrossingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ZeroCrossingError';
  }
}

export function interval(lo: Bound, hi: Bound): Interval {
  if (lo !== null && hi !== null && lo.cmp(hi) > 0) return EMPTY;
  return { kind: 'bounded', lo, hi };
}

export function point(d: Decimal): Interval {
  return interval(d, d);
}

export function isEmpty(iv: Interval): boolean {
  return iv.kind === 'empty';
}

export function isUnknown(iv: Interval): boolean {
  return iv.kind === 'bounded' && iv.lo === null && iv.hi === null;
}

/** lo-side comparison where null = -inf. */
export function cmpLo(a: Bound, b: Bound): number {
  if (a === null) return b === null ? 0 : -1;
  if (b === null) return 1;
  return a.cmp(b);
}

/** hi-side comparison where null = +inf. */
export function cmpHi(a: Bound, b: Bound): number {
  if (a === null) return b === null ? 0 : 1;
  if (b === null) return -1;
  return a.cmp(b);
}

export function containsPoint(iv: Interval, p: Decimal): boolean {
  if (iv.kind === 'empty') return false;
  return cmpLo(iv.lo, p) <= 0 && cmpHi(iv.hi, p) >= 0;
}

export function containsInterval(outer: Interval, inner: Interval): boolean {
  if (inner.kind === 'empty') return true;
  if (outer.kind === 'empty') return false;
  return cmpLo(outer.lo, inner.lo) <= 0 && cmpHi(outer.hi, inner.hi) >= 0;
}

export function intersect(a: Interval, b: Interval): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  const lo = cmpLo(a.lo, b.lo) >= 0 ? a.lo : b.lo;
  const hi = cmpHi(a.hi, b.hi) <= 0 ? a.hi : b.hi;
  return interval(lo, hi);
}

/** Convex hull; the empty interval is the identity element. */
export function hull(a: Interval, b: Interval): Interval {
  if (a.kind === 'empty') return b;
  if (b.kind === 'empty') return a;
  const lo = cmpLo(a.lo, b.lo) <= 0 ? a.lo : b.lo;
  const hi = cmpHi(a.hi, b.hi) >= 0 ? a.hi : b.hi;
  return interval(lo, hi);
}

export function neg(iv: Interval): Interval {
  if (iv.kind === 'empty') return EMPTY;
  return interval(iv.hi === null ? null : iv.hi.neg(), iv.lo === null ? null : iv.lo.neg());
}

function emit(certs: IntervalOpCert[] | undefined, cert: IntervalOpCert): void {
  if (certs) certs.push(cert);
}

export function add(a: Interval, b: Interval, scale: number, certs?: IntervalOpCert[]): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  const lo = a.lo !== null && b.lo !== null ? a.lo.add(b.lo).round(scale, 'floor') : null;
  const hi = a.hi !== null && b.hi !== null ? a.hi.add(b.hi).round(scale, 'ceil') : null;
  const out = interval(lo, hi);
  emit(certs, { kind: 'interval-op', op: 'add', lhs: a, rhs: b, scale, segments: [out] });
  return out;
}

export function sub(a: Interval, b: Interval, scale: number, certs?: IntervalOpCert[]): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  const nb = neg(b);
  const out = add(a, nb, scale);
  emit(certs, { kind: 'interval-op', op: 'sub', lhs: a, rhs: b, scale, segments: [out] });
  return out;
}

/** Extended endpoint: inf = -1/0/+1, val set iff inf === 0. */
interface Ext {
  readonly inf: -1 | 0 | 1;
  readonly val: Decimal | null;
}

function loExt(b: Bound): Ext {
  return b === null ? { inf: -1, val: null } : { inf: 0, val: b };
}

function hiExt(b: Bound): Ext {
  return b === null ? { inf: 1, val: null } : { inf: 0, val: b };
}

function extSign(e: Ext): -1 | 0 | 1 {
  return e.inf !== 0 ? e.inf : (e.val as Decimal).sign();
}

/** Extended product; the 0 x inf corner contributes 0 (the zero endpoint is attained). */
function emul(x: Ext, y: Ext): Ext {
  if (x.inf === 0 && y.inf === 0) return { inf: 0, val: (x.val as Decimal).mul(y.val as Decimal) };
  const s = extSign(x) * extSign(y);
  if (s === 0) return { inf: 0, val: Decimal.ZERO };
  return { inf: s as -1 | 1, val: null };
}

function cmpExt(a: Ext, b: Ext): number {
  if (a.inf !== b.inf) return a.inf < b.inf ? -1 : 1;
  if (a.inf === 0) return (a.val as Decimal).cmp(b.val as Decimal);
  return 0;
}

export function mul(a: Interval, b: Interval, scale: number, certs?: IntervalOpCert[]): Interval {
  if (a.kind === 'empty' || b.kind === 'empty') return EMPTY;
  const corners: Ext[] = [
    emul(loExt(a.lo), loExt(b.lo)),
    emul(loExt(a.lo), hiExt(b.hi)),
    emul(hiExt(a.hi), loExt(b.lo)),
    emul(hiExt(a.hi), hiExt(b.hi)),
  ];
  let lo = corners[0]!;
  let hi = corners[0]!;
  for (const c of corners) {
    if (cmpExt(c, lo) < 0) lo = c;
    if (cmpExt(c, hi) > 0) hi = c;
  }
  const out = interval(
    lo.inf === 0 ? (lo.val as Decimal).round(scale, 'floor') : null,
    hi.inf === 0 ? (hi.val as Decimal).round(scale, 'ceil') : null,
  );
  emit(certs, { kind: 'interval-op', op: 'mul', lhs: a, rhs: b, scale, segments: [out] });
  return out;
}

/** Exact reciprocal bounds of an interval not containing zero. */
function recipBounds(b: Interval, scale: number): { lo: Bound; hi: Bound } {
  if (b.kind !== 'bounded') throw new Error('unreachable');
  const one = Decimal.ONE;
  const lo = b.hi === null ? Decimal.ZERO : one.div(b.hi, scale, 'floor');
  const hi = b.lo === null ? Decimal.ZERO : one.div(b.lo, scale, 'ceil');
  return { lo, hi };
}

/**
 * Interval division. When the divisor contains zero the result is a union of
 * rays; `onZeroCross` selects the policy:
 *  - 'reject': throw ZeroCrossingError;
 *  - 'split':  return the rays explicitly (1 or 2 segments).
 * A zero-point divisor yields the empty set (undefined everywhere).
 */
export function div(
  a: Interval,
  b: Interval,
  scale: number,
  onZeroCross: 'reject' | 'split',
  certs?: IntervalOpCert[],
): Interval[] {
  if (a.kind === 'empty' || b.kind === 'empty') return [EMPTY];
  const zero = Decimal.ZERO;
  const bIsZeroPoint = b.lo !== null && b.hi !== null && b.lo.isZero() && b.hi.isZero();
  if (bIsZeroPoint) {
    emit(certs, { kind: 'interval-op', op: 'div', lhs: a, rhs: b, scale, segments: [EMPTY] });
    return [EMPTY];
  }
  const crossesZero = containsPoint(b, zero);
  if (!crossesZero) {
    const r = recipBounds(b, scale);
    const out = mul(a, interval(r.lo, r.hi), scale);
    emit(certs, { kind: 'interval-op', op: 'div', lhs: a, rhs: b, scale, segments: [out] });
    return [out];
  }
  if (onZeroCross === 'reject') {
    throw new ZeroCrossingError(`divisor ${show(b)} contains zero (policy: reject)`);
  }
  const pieces: Interval[] = [];
  // y in [b.lo, 0): 1/y in (-inf, 1/b.lo]
  if (b.lo === null || b.lo.sign() < 0) {
    const hi = b.lo === null ? zero : Decimal.ONE.div(b.lo, scale, 'ceil');
    pieces.push(mul(a, interval(null, hi), scale));
  }
  // y in (0, b.hi]: 1/y in [1/b.hi, +inf)
  if (b.hi === null || b.hi.sign() > 0) {
    const lo = b.hi === null ? zero : Decimal.ONE.div(b.hi, scale, 'floor');
    pieces.push(mul(a, interval(lo, null), scale));
  }
  emit(certs, { kind: 'interval-op', op: 'div', lhs: a, rhs: b, scale, segments: pieces });
  return pieces;
}

export function show(iv: Interval): string {
  if (iv.kind === 'empty') return 'EMPTY';
  const lo = iv.lo === null ? '-inf' : iv.lo.toString();
  const hi = iv.hi === null ? '+inf' : iv.hi.toString();
  return `[${lo}, ${hi}]`;
}
