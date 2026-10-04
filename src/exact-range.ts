/**
 * Exact rational interval arithmetic with unbounded endpoints. Used only by
 * the independent certificate checker: it recomputes the true mathematical
 * range of an operation without any rounding, so a certificate's outward
 * rounding direction and containment can be verified exactly.
 */

import { Rational } from './rational.ts';
import type { Interval } from './interval.ts';

export type RBound = Rational | null; // null = infinity on its side

export type RInterval =
  | { readonly kind: 'empty' }
  | { readonly kind: 'bounded'; readonly lo: RBound; readonly hi: RBound };

export const REMPTY: RInterval = { kind: 'empty' };

export function rinterval(lo: RBound, hi: RBound): RInterval {
  if (lo !== null && hi !== null && lo.cmp(hi) > 0) return REMPTY;
  return { kind: 'bounded', lo, hi };
}

export function fromInterval(iv: Interval): RInterval {
  if (iv.kind === 'empty') return REMPTY;
  return {
    kind: 'bounded',
    lo: iv.lo === null ? null : Rational.fromDecimal(iv.lo),
    hi: iv.hi === null ? null : Rational.fromDecimal(iv.hi),
  };
}

export function cmpLoR(a: RBound, b: RBound): number {
  if (a === null) return b === null ? 0 : -1;
  if (b === null) return 1;
  return a.cmp(b);
}

export function cmpHiR(a: RBound, b: RBound): number {
  if (a === null) return b === null ? 0 : 1;
  if (b === null) return -1;
  return a.cmp(b);
}

export function containsRInterval(outer: RInterval, inner: RInterval): boolean {
  if (inner.kind === 'empty') return true;
  if (outer.kind === 'empty') return false;
  return cmpLoR(outer.lo, inner.lo) <= 0 && cmpHiR(outer.hi, inner.hi) >= 0;
}

export function containsRational(iv: RInterval, p: Rational): boolean {
  if (iv.kind === 'empty') return false;
  return cmpLoR(iv.lo, p) <= 0 && cmpHiR(iv.hi, p) >= 0;
}

export function rAdd(a: RInterval, b: RInterval): RInterval {
  if (a.kind === 'empty' || b.kind === 'empty') return REMPTY;
  return rinterval(
    a.lo !== null && b.lo !== null ? a.lo.add(b.lo) : null,
    a.hi !== null && b.hi !== null ? a.hi.add(b.hi) : null,
  );
}

export function rNeg(a: RInterval): RInterval {
  if (a.kind === 'empty') return REMPTY;
  return rinterval(a.hi === null ? null : a.hi.neg(), a.lo === null ? null : a.lo.neg());
}

export function rSub(a: RInterval, b: RInterval): RInterval {
  return rAdd(a, rNeg(b));
}

interface RExt {
  readonly inf: -1 | 0 | 1;
  readonly val: Rational | null;
}

function loExtR(b: RBound): RExt {
  return b === null ? { inf: -1, val: null } : { inf: 0, val: b };
}

function hiExtR(b: RBound): RExt {
  return b === null ? { inf: 1, val: null } : { inf: 0, val: b };
}

function extSignR(e: RExt): -1 | 0 | 1 {
  return e.inf !== 0 ? e.inf : (e.val as Rational).sign();
}

function emulR(x: RExt, y: RExt): RExt {
  if (x.inf === 0 && y.inf === 0) return { inf: 0, val: (x.val as Rational).mul(y.val as Rational) };
  const s = extSignR(x) * extSignR(y);
  if (s === 0) return { inf: 0, val: Rational.ZERO };
  return { inf: s as -1 | 1, val: null };
}

function cmpExtR(a: RExt, b: RExt): number {
  if (a.inf !== b.inf) return a.inf < b.inf ? -1 : 1;
  if (a.inf === 0) return (a.val as Rational).cmp(b.val as Rational);
  return 0;
}

export function rMul(a: RInterval, b: RInterval): RInterval {
  if (a.kind === 'empty' || b.kind === 'empty') return REMPTY;
  const corners: RExt[] = [
    emulR(loExtR(a.lo), loExtR(b.lo)),
    emulR(loExtR(a.lo), hiExtR(b.hi)),
    emulR(hiExtR(a.hi), loExtR(b.lo)),
    emulR(hiExtR(a.hi), hiExtR(b.hi)),
  ];
  let lo = corners[0]!;
  let hi = corners[0]!;
  for (const c of corners) {
    if (cmpExtR(c, lo) < 0) lo = c;
    if (cmpExtR(c, hi) > 0) hi = c;
  }
  return rinterval(
    lo.inf === 0 ? lo.val : null,
    hi.inf === 0 ? hi.val : null,
  );
}

function rRecip(b: RInterval): RInterval {
  if (b.kind !== 'bounded') throw new Error('unreachable');
  const lo = b.hi === null ? Rational.ZERO : Rational.ONE.div(b.hi);
  const hi = b.lo === null ? Rational.ZERO : Rational.ONE.div(b.lo);
  return rinterval(lo, hi);
}

function rContainsZero(b: RInterval): boolean {
  if (b.kind === 'empty') return false;
  return cmpLoR(b.lo, Rational.ZERO) <= 0 && cmpHiR(b.hi, Rational.ZERO) >= 0;
}

/** Exact division; may return two rays when the divisor contains zero. */
export function rDiv(a: RInterval, b: RInterval): RInterval[] {
  if (a.kind === 'empty' || b.kind === 'empty') return [REMPY];
  if (b.lo !== null && b.hi !== null && b.lo.isZero() && b.hi.isZero()) return [REMPY];
  if (!rContainsZero(b)) return [rMul(a, rRecip(b))];
  const pieces: RInterval[] = [];
  if (b.lo === null || b.lo.sign() < 0) {
    const hi = b.lo === null ? Rational.ZERO : Rational.ONE.div(b.lo);
    pieces.push(rMul(a, rinterval(null, hi)));
  }
  if (b.hi === null || b.hi.sign() > 0) {
    const lo = b.hi === null ? Rational.ZERO : Rational.ONE.div(b.hi);
    pieces.push(rMul(a, rinterval(lo, null)));
  }
  return pieces;
}

/** True mathematical range of an interval operation, as 1-2 exact pieces. */
export function exactRange(
  op: 'add' | 'sub' | 'mul' | 'div',
  lhs: Interval,
  rhs: Interval,
): RInterval[] {
  const a = fromInterval(lhs);
  const b = fromInterval(rhs);
  switch (op) {
    case 'add': return [rAdd(a, b)];
    case 'sub': return [rSub(a, b)];
    case 'mul': return [rMul(a, b)];
    case 'div': return rDiv(a, b);
  }
}

export function showR(iv: RInterval): string {
  if (iv.kind === 'empty') return 'EMPTY';
  const lo = iv.lo === null ? '-inf' : iv.lo.toString();
  const hi = iv.hi === null ? '+inf' : iv.hi.toString();
  return `[${lo}, ${hi}]`;
}
