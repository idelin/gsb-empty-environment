/**
 * Independent checker. Recomputes, with exact rationals and code paths
 * separate from the arithmetic implementation:
 *   - outward rounding direction (result.lo <= exact min, result.hi >= exact max)
 *   - containment of the exact range
 *   - significant-digit precision limits
 *   - remainder certificates from affine nonlinear steps
 * and runs an exact rational counterexample search: sampled true values are
 * evaluated exactly and must lie inside the reported bounds.
 */

import { Dec, numDigits } from './decimal.ts';
import { Rat } from './rational.ts';
import * as Iv from './interval.ts';
import type { Affine, Certificate } from './affine.ts';

export type BinOp = 'add' | 'sub' | 'mul' | 'div';

export interface Counterexample {
  readonly point: string;
  readonly value: string;
}

export interface OpCheck {
  readonly ok: boolean;
  readonly loOutward: boolean;
  readonly hiOutward: boolean;
  readonly withinPrecision: boolean;
  readonly counterexample: Counterexample | null;
  readonly details: readonly string[];
}

function toRatRange(iv: Iv.Interval): { lo: Rat; hi: Rat } | null {
  if (iv.kind !== 'range' || iv.lo === null || iv.hi === null) return null;
  return { lo: Rat.fromDec(iv.lo), hi: Rat.fromDec(iv.hi) };
}

function ratMinAll(xs: Rat[]): Rat {
  return xs.reduce((a, b) => Rat.min(a, b));
}

function ratMaxAll(xs: Rat[]): Rat {
  return xs.reduce((a, b) => Rat.max(a, b));
}

/** Exact (unrounded) range of op over two finite intervals, or null if N/A. */
export function exactRange(op: BinOp, a: Iv.Interval, b: Iv.Interval): { lo: Rat; hi: Rat } | null {
  const A = toRatRange(a);
  const B = toRatRange(b);
  if (A === null || B === null) return null;
  switch (op) {
    case 'add':
      return { lo: Rat.add(A.lo, B.lo), hi: Rat.add(A.hi, B.hi) };
    case 'sub':
      return { lo: Rat.sub(A.lo, B.hi), hi: Rat.sub(A.hi, B.lo) };
    case 'mul': {
      const ps = [Rat.mul(A.lo, B.lo), Rat.mul(A.lo, B.hi), Rat.mul(A.hi, B.lo), Rat.mul(A.hi, B.hi)];
      return { lo: ratMinAll(ps), hi: ratMaxAll(ps) };
    }
    case 'div': {
      if (Rat.cmp(B.lo, Rat.zero) <= 0 && Rat.cmp(B.hi, Rat.zero) >= 0) return null;
      const qs = [Rat.div(A.lo, B.lo), Rat.div(A.lo, B.hi), Rat.div(A.hi, B.lo), Rat.div(A.hi, B.hi)];
      return { lo: ratMinAll(qs), hi: ratMaxAll(qs) };
    }
  }
}

/** Exact membership test: is the rational value inside the interval? */
export function containsRat(iv: Iv.Interval, v: Rat): boolean {
  if (iv.kind === 'empty') return false;
  if (iv.kind === 'unknown') return true;
  if (iv.lo !== null && Rat.cmp(v, Rat.fromDec(iv.lo)) < 0) return false;
  if (iv.hi !== null && Rat.cmp(v, Rat.fromDec(iv.hi)) > 0) return false;
  return true;
}

export function evalExact(op: BinOp, x: Rat, y: Rat): Rat | null {
  switch (op) {
    case 'add':
      return Rat.add(x, y);
    case 'sub':
      return Rat.sub(x, y);
    case 'mul':
      return Rat.mul(x, y);
    case 'div':
      return y.sign() === 0 ? null : Rat.div(x, y);
  }
}

function lerp(lo: Rat, hi: Rat, i: number, n: number): Rat {
  return Rat.add(lo, Rat.div(Rat.mul(Rat.sub(hi, lo), Rat.of(BigInt(i))), Rat.of(BigInt(n))));
}

/**
 * Exact rational counterexample search: sample a grid of true input values,
 * evaluate the operation exactly, and return the first sample whose true
 * result falls outside the reported interval.
 */
export function findCounterexample(
  op: BinOp,
  a: Iv.Interval,
  b: Iv.Interval,
  result: Iv.Interval,
  grid = 8,
): Counterexample | null {
  const A = toRatRange(a);
  const B = toRatRange(b);
  if (A === null || B === null) return null;
  for (let i = 0; i <= grid; i++) {
    const x = lerp(A.lo, A.hi, i, grid);
    for (let j = 0; j <= grid; j++) {
      const y = lerp(B.lo, B.hi, j, grid);
      const v = evalExact(op, x, y);
      if (v === null) continue;
      if (!containsRat(result, v)) {
        return { point: `(${x.toString()}, ${y.toString()})`, value: v.toString() };
      }
    }
  }
  return null;
}

/** Full independent check of one binary interval operation. */
export function checkBinaryOp(op: BinOp, a: Iv.Interval, b: Iv.Interval, result: Iv.Interval, prec: number): OpCheck {
  const details: string[] = [];
  let loOutward = true;
  let hiOutward = true;
  let withinPrecision = true;
  const exact = exactRange(op, a, b);
  if (exact !== null && result.kind === 'range') {
    if (result.lo !== null) loOutward = Rat.cmp(Rat.fromDec(result.lo), exact.lo) <= 0;
    if (result.hi !== null) hiOutward = Rat.cmp(Rat.fromDec(result.hi), exact.hi) >= 0;
    details.push(`exact range [${exact.lo.toString()}, ${exact.hi.toString()}]`);
  }
  if (result.kind === 'range') {
    for (const bound of [result.lo, result.hi]) {
      if (bound !== null && numDigits(bound.m) > prec) withinPrecision = false;
    }
  }
  const counterexample = findCounterexample(op, a, b, result);
  const ok = loOutward && hiOutward && withinPrecision && counterexample === null;
  return { ok, loOutward, hiOutward, withinPrecision, counterexample, details };
}

export interface CertCheck {
  readonly ok: boolean;
  readonly detail: string;
}

/**
 * Verify a remainder certificate independently:
 *  - mul: remainder must be >= radiusA * radiusB (exact decimal product)
 *  - recip: |1/x - k*x - d| <= remainder must hold over [lo, hi]; checked
 *    exactly at rational sample points (counterexample search).
 */
export function checkCertificate(cert: Certificate, samples = 32): CertCheck {
  if (cert.op === 'mul') {
    const need = Dec.mul(cert.radiusA, cert.radiusB);
    const ok = Dec.cmp(cert.remainder, need) >= 0;
    return { ok, detail: `mul: remainder ${cert.remainder.toString()} vs required ${need.toString()}` };
  }
  const L = Rat.fromDec(cert.lo);
  const H = Rat.fromDec(cert.hi);
  const k = Rat.fromDec(cert.k);
  const d = Rat.fromDec(cert.d);
  const R = Rat.fromDec(cert.remainder);
  const residual = (x: Rat): Rat =>
    Rat.abs(Rat.sub(Rat.sub(Rat.div(Rat.one, x), Rat.mul(k, x)), d));
  for (let i = 0; i <= samples; i++) {
    const x = lerp(L, H, i, samples);
    if (Rat.cmp(residual(x), R) > 0) {
      return { ok: false, detail: `recip: residual exceeded at x=${x.toString()}` };
    }
  }
  return {
    ok: true,
    detail: `recip: remainder ${cert.remainder.toString()} covers residual on [${cert.lo.toString()}, ${cert.hi.toString()}]`,
  };
}

/** Evaluate an affine form exactly under a rational noise assignment. */
export function evalAffineExact(aff: Affine, eps: ReadonlyMap<number, Rat>): Rat {
  let v = Rat.fromDec(aff.c0);
  for (const [sym, coef] of aff.terms) {
    const e = eps.get(sym);
    if (e !== undefined) v = Rat.add(v, Rat.mul(Rat.fromDec(coef), e));
  }
  return v;
}
