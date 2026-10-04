/**
 * Affine arithmetic: each quantity is  c0 + sum_i ci * eps_i  with eps_i in
 * [-1, 1]. Noise symbols are shared by construction, so when the same
 * uncertain input appears twice the correlation is preserved and linear
 * operations stay exact (no false widening: x - x = 0).
 *
 * Nonlinear steps (mul, recip) push the nonlinear residual onto a fresh
 * noise symbol and record a conservative remainder certificate that the
 * independent checker (certificate.ts) can verify with exact rationals.
 */

import { Dec } from './decimal.ts';
import { Rat } from './rational.ts';
import * as Iv from './interval.ts';

export type Certificate =
  | {
      readonly op: 'mul';
      readonly sym: number;
      readonly remainder: Dec;
      readonly radiusA: Dec;
      readonly radiusB: Dec;
    }
  | {
      readonly op: 'recip';
      readonly sym: number;
      readonly remainder: Dec;
      readonly lo: Dec;
      readonly hi: Dec;
      readonly k: Dec;
      readonly d: Dec;
    };

export class AffineCtx {
  private nextSym = 0;
  readonly certificates: Certificate[] = [];

  readonly prec: number;

  constructor(prec: number) {
    if (!Number.isInteger(prec) || prec < 1) throw new RangeError('prec must be an integer >= 1');
    this.prec = prec;
  }

  fresh(): number {
    return this.nextSym++;
  }

  constant(d: Dec): Affine {
    return new Affine(d, new Map());
  }

  /** An uncertain input ranging over [lo, hi]; midpoint and radius are exact. */
  variable(lo: Dec, hi: Dec): Affine {
    if (Dec.cmp(lo, hi) > 0) throw new RangeError('variable: lo > hi');
    const mid = Dec.halve(Dec.add(lo, hi));
    const radius = Dec.halve(Dec.sub(hi, lo));
    const id = this.fresh();
    return new Affine(mid, new Map([[id, radius]]));
  }
}

function addTerm(terms: Map<number, Dec>, sym: number, coef: Dec): void {
  const next = Dec.add(terms.get(sym) ?? Dec.zero, coef);
  if (Dec.isZero(next)) terms.delete(sym);
  else terms.set(sym, next);
}

export class Affine {
  readonly c0: Dec;
  readonly terms: ReadonlyMap<number, Dec>;

  constructor(c0: Dec, terms: ReadonlyMap<number, Dec>) {
    this.c0 = c0;
    this.terms = terms;
  }

  /** Exact. */
  static add(a: Affine, b: Affine): Affine {
    const terms = new Map(a.terms);
    for (const [sym, coef] of b.terms) addTerm(terms, sym, coef);
    return new Affine(Dec.add(a.c0, b.c0), terms);
  }

  /** Exact. */
  static neg(a: Affine): Affine {
    const terms = new Map<number, Dec>();
    for (const [sym, coef] of a.terms) terms.set(sym, Dec.neg(coef));
    return new Affine(Dec.neg(a.c0), terms);
  }

  /** Exact. */
  static sub(a: Affine, b: Affine): Affine {
    return Affine.add(a, Affine.neg(b));
  }

  /** Exact; a negative constant flips every coefficient's sign. */
  static scale(a: Affine, k: Dec): Affine {
    const terms = new Map<number, Dec>();
    for (const [sym, coef] of a.terms) addTerm(terms, sym, Dec.mul(coef, k));
    return new Affine(Dec.mul(a.c0, k), terms);
  }

  /** Sum of |ci|, exact. */
  radius(): Dec {
    let r = Dec.zero;
    for (const coef of this.terms.values()) r = Dec.add(r, Dec.abs(coef));
    return r;
  }

  /** Exact (unrounded) bounds. */
  exactBounds(): { lo: Dec; hi: Dec } {
    const r = this.radius();
    return { lo: Dec.sub(this.c0, r), hi: Dec.add(this.c0, r) };
  }

  /** Bounds rounded outward to `prec` significant digits. */
  toInterval(prec: number): Iv.Interval {
    const { lo, hi } = this.exactBounds();
    return Iv.range(Dec.round(lo, prec, 'floor'), Dec.round(hi, prec, 'ceil'));
  }

  /**
   * a * b. The quadratic residual (sum ai*ei)(sum bj*ej) is bounded by
   * radius(a)*radius(b) and carried by one fresh noise symbol.
   */
  static mul(ctx: AffineCtx, a: Affine, b: Affine): Affine {
    const terms = new Map<number, Dec>();
    for (const [sym, coef] of a.terms) addTerm(terms, sym, Dec.mul(coef, b.c0));
    for (const [sym, coef] of b.terms) addTerm(terms, sym, Dec.mul(coef, a.c0));
    const radiusA = a.radius();
    const radiusB = b.radius();
    const remainder = Dec.mul(radiusA, radiusB);
    if (!Dec.isZero(remainder)) {
      const sym = ctx.fresh();
      terms.set(sym, remainder);
      ctx.certificates.push({ op: 'mul', sym, remainder, radiusA, radiusB });
    }
    return new Affine(Dec.mul(a.c0, b.c0), terms);
  }

  /**
   * 1 / a. Rejects (throws ZeroDivisionError) when the exact bounds of a
   * contain zero. Otherwise fits the secant line k*x + d of 1/x over
   * [lo, hi] and bounds the residual exactly with rationals; the bound is
   * rounded UP and carried by a fresh noise symbol.
   */
  static recip(ctx: AffineCtx, a: Affine): Affine {
    const { lo, hi } = a.exactBounds();
    if (Dec.cmp(lo, Dec.zero) <= 0 && Dec.cmp(hi, Dec.zero) >= 0) {
      throw new Iv.ZeroDivisionError('affine recip: denominator range contains 0');
    }
    const L = Rat.fromDec(lo);
    const H = Rat.fromDec(hi);
    // Working slope of 1/x on [L, H]: -1/(L*H), rounded to the context precision.
    const k = Rat.neg(Rat.div(Rat.one, Rat.mul(L, H))).toDec(ctx.prec, 'half-even');
    const kR = Rat.fromDec(k);
    // Exact rational bounds of g(x) = 1/x - k*x over [L, H] (interval evaluation).
    const recipLo = Rat.div(Rat.one, H);
    const recipHi = Rat.div(Rat.one, L);
    const kP1 = Rat.mul(kR, L);
    const kP2 = Rat.mul(kR, H);
    const kLo = Rat.min(kP1, kP2);
    const kHi = Rat.max(kP1, kP2);
    const gLo = Rat.sub(recipLo, kHi);
    const gHi = Rat.sub(recipHi, kLo);
    const d = Rat.div(Rat.add(gLo, gHi), Rat.of(2n)).toDec(ctx.prec, 'half-even');
    const dR = Rat.fromDec(d);
    const errLo = Rat.abs(Rat.sub(gLo, dR));
    const errHi = Rat.abs(Rat.sub(gHi, dR));
    const remainder = Rat.max(errLo, errHi).toDec(ctx.prec, 'ceil'); // rounded UP: conservative
    const sym = ctx.fresh();
    ctx.certificates.push({ op: 'recip', sym, remainder, lo, hi, k, d });
    const scaled = Affine.scale(a, k);
    const terms = new Map(scaled.terms);
    addTerm(terms, sym, remainder);
    return new Affine(Dec.add(scaled.c0, d), terms);
  }

  /** a / b = a * (1 / b); rejects when b's range contains zero. */
  static div(ctx: AffineCtx, a: Affine, b: Affine): Affine {
    return Affine.mul(ctx, a, Affine.recip(ctx, b));
  }
}
