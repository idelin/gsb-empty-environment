/**
 * Affine arithmetic: x = center + sum_i coeff_i * eps_i with eps_i in [-1, 1].
 * The same uncertain input reuses the same noise symbol id, so correlated
 * terms cancel exactly in linear operations (no false widening). Nonlinear
 * steps (mul, recip) introduce a fresh noise symbol whose magnitude is a
 * conservative, certified remainder bound.
 */

import { Decimal } from './decimal.ts';
import type { Interval } from './interval.ts';
import { ZeroCrossingError, div as idiv, interval, mul as imul, point, sub as isub } from './interval.ts';

export interface AffineMulCert {
  readonly kind: 'affine-mul';
  readonly scale: number;
  readonly lhsCenter: Decimal;
  readonly lhsTerms: readonly [number, Decimal][];
  readonly rhsCenter: Decimal;
  readonly rhsTerms: readonly [number, Decimal][];
  readonly remainder: Decimal; // coefficient of the fresh symbol, rounded away from 0
  readonly symbol: number;
  readonly resultCenter: Decimal;
  readonly resultTerms: readonly [number, Decimal][];
}

export interface AffineRecipCert {
  readonly kind: 'affine-recip';
  readonly scale: number;
  readonly lo: Decimal; // exact input range bounds
  readonly hi: Decimal;
  readonly alpha: Decimal; // secant slope actually used
  readonly zeta: Decimal;  // intercept actually used
  readonly delta: Decimal; // certified |1/x - alpha*x - zeta| bound on [lo, hi]
  readonly symbol: number;
  readonly inputCenter: Decimal;
  readonly inputTerms: readonly [number, Decimal][];
  readonly resultCenter: Decimal;
  readonly resultTerms: readonly [number, Decimal][];
}

export type AffineCert = AffineMulCert | AffineRecipCert;

const GUARD_DIGITS = 10;

let nextSymbolId = 1;

export function freshSymbol(): number {
  return nextSymbolId++;
}

export class AffineForm {
  readonly center: Decimal;
  readonly terms: ReadonlyMap<number, Decimal>;

  constructor(center: Decimal, terms: ReadonlyMap<number, Decimal>) {
    this.center = center;
    const clean = new Map<number, Decimal>();
    for (const [k, v] of terms) if (!v.isZero()) clean.set(k, v);
    this.terms = clean;
  }

  static constant(d: Decimal): AffineForm {
    return new AffineForm(d, new Map());
  }

  /** One uncertain input: a fresh noise symbol shared by every later use. */
  static fromInterval(iv: Interval, symbol: number = freshSymbol()): AffineForm {
    if (iv.kind !== 'bounded' || iv.lo === null || iv.hi === null) {
      throw new RangeError('AffineForm.fromInterval: needs a finite bounded interval');
    }
    const center = iv.lo.add(iv.hi).divExact2();
    const radius = iv.hi.sub(iv.lo).divExact2();
    return new AffineForm(center, new Map([[symbol, radius]]));
  }

  radius(): Decimal {
    let r = Decimal.ZERO;
    for (const c of this.terms.values()) r = r.add(c.abs());
    return r;
  }

  /** Exact (unrounded) range. */
  rangeExact(): { lo: Decimal; hi: Decimal } {
    const r = this.radius();
    return { lo: this.center.sub(r), hi: this.center.add(r) };
  }

  /** Range rounded outward to `scale` fractional digits. */
  toInterval(scale: number): Interval {
    const { lo, hi } = this.rangeExact();
    return interval(lo.round(scale, 'floor'), hi.round(scale, 'ceil'));
  }

  /** Exact point evaluation under a noise assignment. */
  evaluate(assignment: ReadonlyMap<number, Decimal>): Decimal {
    let v = this.center;
    for (const [k, c] of this.terms) {
      const e = assignment.get(k);
      if (e !== undefined) v = v.add(c.mul(e));
    }
    return v;
  }

  add(o: AffineForm): AffineForm {
    const terms = new Map<number, Decimal>(this.terms);
    for (const [k, c] of o.terms) {
      const prev = terms.get(k);
      terms.set(k, prev === undefined ? c : prev.add(c));
    }
    return new AffineForm(this.center.add(o.center), terms);
  }

  neg(): AffineForm {
    const terms = new Map<number, Decimal>();
    for (const [k, c] of this.terms) terms.set(k, c.neg());
    return new AffineForm(this.center.neg(), terms);
  }

  sub(o: AffineForm): AffineForm {
    return this.add(o.neg());
  }

  /** Exact scaling; a negative factor flips every coefficient's sign. */
  scaleBy(k: Decimal): AffineForm {
    const terms = new Map<number, Decimal>();
    for (const [id, c] of this.terms) terms.set(id, c.mul(k));
    return new AffineForm(this.center.mul(k), terms);
  }

  /**
   * Product of two affine forms. Linear parts are exact; the joint nonlinear
   * error is bounded by radiusA * radiusB and attached to a fresh symbol.
   */
  mul(o: AffineForm, scale: number): { form: AffineForm; cert: AffineMulCert } {
    const ra = this.radius();
    const rb = o.radius();
    const remainder = ra.mul(rb).round(scale, 'up');
    const symbol = freshSymbol();
    const terms = new Map<number, Decimal>();
    const keys = new Set<number>([...this.terms.keys(), ...o.terms.keys()]);
    for (const k of keys) {
      const ai = this.terms.get(k) ?? Decimal.ZERO;
      const bi = o.terms.get(k) ?? Decimal.ZERO;
      const coeff = this.center.mul(bi).add(o.center.mul(ai));
      if (!coeff.isZero()) terms.set(k, coeff);
    }
    if (!remainder.isZero()) terms.set(symbol, remainder);
    const form = new AffineForm(this.center.mul(o.center), terms);
    const cert: AffineMulCert = {
      kind: 'affine-mul',
      scale,
      lhsCenter: this.center,
      lhsTerms: [...this.terms.entries()],
      rhsCenter: o.center,
      rhsTerms: [...o.terms.entries()],
      remainder,
      symbol,
      resultCenter: form.center,
      resultTerms: [...form.terms.entries()],
    };
    return { form, cert };
  }

  /**
   * Affine reciprocal 1/x via a secant-line approximation alpha*x + zeta with
   * a certified remainder delta covering |1/x - alpha*x - zeta| on the exact
   * input range. Rejects (throws) when the range contains zero.
   */
  recip(scale: number): { form: AffineForm; cert: AffineRecipCert } {
    const { lo, hi } = this.rangeExact();
    if (lo.sign() === 0 || hi.sign() === 0 || lo.sign() !== hi.sign()) {
      throw new ZeroCrossingError(`reciprocal rejected: range [${lo}, ${hi}] contains zero`);
    }
    const guard = scale + GUARD_DIGITS;
    // Secant slope of 1/x on [lo, hi]: -1/(lo*hi); any rounding is safe
    // because delta below absorbs the discrepancy.
    const alpha = Decimal.ONE.neg().div(lo.mul(hi), guard, 'down');
    // Bound g(x) = 1/x - alpha*x over [lo, hi] with outward interval arithmetic.
    const dom = interval(lo, hi);
    const recipRange = idiv(point(Decimal.ONE), dom, guard, 'reject')[0]!;
    const alphaX = imul(point(alpha), dom, guard);
    const g = isub(recipRange, alphaX, guard);
    if (g.kind !== 'bounded' || g.lo === null || g.hi === null) throw new Error('unreachable');
    const zeta = g.lo.add(g.hi).divExact2();
    const delta = g.hi.sub(g.lo).divExact2().round(scale, 'up');
    const symbol = freshSymbol();
    const scaled = this.scaleBy(alpha);
    const terms = new Map<number, Decimal>(scaled.terms);
    if (!delta.isZero()) terms.set(symbol, delta);
    const form = new AffineForm(scaled.center.add(zeta), terms);
    const cert: AffineRecipCert = {
      kind: 'affine-recip',
      scale,
      lo,
      hi,
      alpha,
      zeta,
      delta,
      symbol,
      inputCenter: this.center,
      inputTerms: [...this.terms.entries()],
      resultCenter: form.center,
      resultTerms: [...form.terms.entries()],
    };
    return { form, cert };
  }

  /** Quotient via reciprocal; rejects divisors whose range contains zero. */
  div(o: AffineForm, scale: number): { form: AffineForm; certs: AffineCert[] } {
    const r = o.recip(scale);
    const m = this.mul(r.form, scale);
    return { form: m.form, certs: [r.cert, m.cert] };
  }

  toString(): string {
    const parts = [`${this.center}`];
    for (const [k, c] of [...this.terms.entries()].sort((a, b) => a[0] - b[0])) {
      parts.push(`${c}*e${k}`);
    }
    return parts.join(' + ');
  }
}
