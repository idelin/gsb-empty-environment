/**
 * Univariate polynomials with exact rational coefficients,
 * plus Sturm sequences and real-root isolation (all exact, no floats).
 */
import { Rational, lcmBig } from './rational.ts';

export class Poly {
  /** coeffs[i] = coefficient of x^i; no trailing zeros; zero polynomial = []. */
  readonly coeffs: readonly Rational[];

  private constructor(coeffs: Rational[]) {
    this.coeffs = coeffs;
  }

  static of(coeffs: readonly (Rational | number | bigint)[]): Poly {
    const cs = coeffs.map((c) => Rational.of(c));
    let end = cs.length;
    while (end > 0 && cs[end - 1].isZero()) end--;
    return new Poly(cs.slice(0, end));
  }

  static zero(): Poly {
    return new Poly([]);
  }
  static one(): Poly {
    return new Poly([Rational.ONE]);
  }
  static constant(c: Rational | number | bigint): Poly {
    const r = Rational.of(c);
    return r.isZero() ? Poly.zero() : new Poly([r]);
  }
  static x(): Poly {
    return new Poly([Rational.ZERO, Rational.ONE]);
  }

  get deg(): number {
    return this.coeffs.length - 1;
  }
  isZero(): boolean {
    return this.coeffs.length === 0;
  }
  isOne(): boolean {
    return this.coeffs.length === 1 && this.coeffs[0].isOne();
  }
  lc(): Rational {
    if (this.isZero()) throw new Error('lc of zero polynomial');
    return this.coeffs[this.coeffs.length - 1];
  }

  add(o: Poly): Poly {
    const n = Math.max(this.coeffs.length, o.coeffs.length);
    const out: Rational[] = [];
    for (let i = 0; i < n; i++) {
      const a = i < this.coeffs.length ? this.coeffs[i] : Rational.ZERO;
      const b = i < o.coeffs.length ? o.coeffs[i] : Rational.ZERO;
      out.push(a.add(b));
    }
    return Poly.of(out);
  }
  sub(o: Poly): Poly {
    return this.add(o.neg());
  }
  neg(): Poly {
    return Poly.of(this.coeffs.map((c) => c.neg()));
  }
  mul(o: Poly): Poly {
    if (this.isZero() || o.isZero()) return Poly.zero();
    const out: Rational[] = new Array(this.deg + o.deg + 1).fill(Rational.ZERO);
    for (let i = 0; i < this.coeffs.length; i++) {
      for (let j = 0; j < o.coeffs.length; j++) {
        out[i + j] = out[i + j].add(this.coeffs[i].mul(o.coeffs[j]));
      }
    }
    return Poly.of(out);
  }
  scale(c: Rational): Poly {
    if (c.isZero()) return Poly.zero();
    return Poly.of(this.coeffs.map((k) => k.mul(c)));
  }
  /** multiply by x^k */
  shift(k: number): Poly {
    if (this.isZero()) return this;
    return Poly.of([...new Array(k).fill(Rational.ZERO), ...this.coeffs]);
  }

  divmod(o: Poly): [Poly, Poly] {
    if (o.isZero()) throw new Error('polynomial division by zero');
    const rem = this.coeffs.slice();
    const degO = o.deg;
    const invLc = o.lc().inv();
    const quotLen = this.deg - degO + 1;
    const quot: Rational[] = new Array(Math.max(quotLen, 0)).fill(Rational.ZERO);
    while (rem.length - 1 >= degO && rem.length > 0) {
      const c = rem[rem.length - 1].mul(invLc);
      const shiftBy = rem.length - 1 - degO;
      quot[shiftBy] = c;
      for (let i = 0; i <= degO; i++) {
        rem[i + shiftBy] = rem[i + shiftBy].sub(c.mul(o.coeffs[i]));
      }
      while (rem.length > 0 && rem[rem.length - 1].isZero()) rem.pop();
    }
    return [Poly.of(quot), Poly.of(rem)];
  }
  div(o: Poly): Poly {
    const [q, r] = this.divmod(o);
    if (!r.isZero()) throw new Error('Poly.div: inexact division');
    return q;
  }
  mod(o: Poly): Poly {
    return this.divmod(o)[1];
  }
  gcd(o: Poly): Poly {
    let a = this;
    let b = o;
    while (!b.isZero()) {
      const r = a.mod(b);
      a = b;
      b = r;
    }
    return a.isZero() ? a : a.monic();
  }

  derivative(): Poly {
    const out: Rational[] = [];
    for (let i = 1; i < this.coeffs.length; i++) {
      out.push(this.coeffs[i].mul(Rational.make(BigInt(i), 1n)));
    }
    return Poly.of(out);
  }
  monic(): Poly {
    if (this.isZero()) return this;
    return this.scale(this.lc().inv());
  }
  /** square-free part: this / gcd(this, this') */
  squareFree(): Poly {
    if (this.deg <= 0) return this;
    return this.div(this.gcd(this.derivative()));
  }

  evaluate(x: Rational): Rational {
    let acc = Rational.ZERO;
    for (let i = this.coeffs.length - 1; i >= 0; i--) {
      acc = acc.mul(x).add(this.coeffs[i]);
    }
    return acc;
  }
  signAt(x: Rational): number {
    return this.evaluate(x).sign();
  }

  equals(o: Poly): boolean {
    if (this.coeffs.length !== o.coeffs.length) return false;
    for (let i = 0; i < this.coeffs.length; i++) {
      if (!this.coeffs[i].equals(o.coeffs[i])) return false;
    }
    return true;
  }

  /** p(-x): roots are negated */
  negateX(): Poly {
    return Poly.of(this.coeffs.map((c, i) => (i % 2 === 1 ? c.neg() : c)));
  }
  /** p(x^2): if p(t) = 0 then this vanishes at sqrt(t) */
  composeX2(): Poly {
    const out: Rational[] = new Array(2 * this.deg + 1).fill(Rational.ZERO);
    for (let i = 0; i < this.coeffs.length; i++) out[2 * i] = this.coeffs[i];
    return Poly.of(out);
  }
  /** x^deg * p(1/x): roots are inverted */
  reciprocal(): Poly {
    return Poly.of(this.coeffs.slice().reverse());
  }

  toString(): string {
    if (this.isZero()) return '0';
    const parts: string[] = [];
    for (let i = this.deg; i >= 0; i--) {
      const c = this.coeffs[i];
      if (c.isZero()) continue;
      const abs = c.abs();
      const sign = c.sign() < 0 ? '-' : '+';
      let body: string;
      if (i === 0) {
        body = abs.toString();
      } else {
        let coef = '';
        if (!abs.isOne()) coef = abs.den === 1n ? abs.toString() : `(${abs.toString()})`;
        body = coef + (i === 1 ? 'x' : `x^${i}`);
      }
      parts.push((parts.length === 0 ? (sign === '-' ? '-' : '') : ` ${sign} `) + body);
    }
    return parts.join('');
  }
}

/** exact integer coefficients of f after clearing denominators (not necessarily primitive) */
export function toIntegerCoeffs(f: Poly): bigint[] {
  let L = 1n;
  for (const c of f.coeffs) L = lcmBig(L, c.den);
  return f.coeffs.map((c) => c.num * (L / c.den));
}

/* ------------------------- Sturm sequences ------------------------- */

export function sturmSequence(f: Poly): Poly[] {
  const seq: Poly[] = [f];
  if (f.deg >= 1) seq.push(f.derivative());
  while (seq[seq.length - 1].deg > 0) {
    const next = seq[seq.length - 2].mod(seq[seq.length - 1]).neg();
    if (next.isZero()) break;
    seq.push(next);
  }
  return seq;
}

/** sign variations of the Sturm sequence evaluated at x (zeros skipped) */
export function signVariations(seq: Poly[], x: Rational): number {
  let count = 0;
  let prev = 0;
  for (const p of seq) {
    const s = p.signAt(x);
    if (s === 0) continue;
    if (prev !== 0 && s !== prev) count++;
    prev = s;
  }
  return count;
}

/* ------------------------ Root isolation ------------------------ */

export interface Interval {
  lo: Rational;
  hi: Rational;
}

/** Cauchy bound: every real root r satisfies |r| < B (strict). */
function cauchyBound(f: Poly): Rational {
  const lc = f.lc().abs();
  let max = Rational.ZERO;
  for (let i = 0; i < f.deg; i++) {
    const r = f.coeffs[i].abs().div(lc);
    if (r.cmp(max) > 0) max = r;
  }
  return max.add(Rational.ONE);
}

function positiveDivisors(n: bigint): bigint[] {
  n = n < 0n ? -n : n;
  const out: bigint[] = [];
  for (let d = 1n; d * d <= n; d++) {
    if (n % d === 0n) {
      out.push(d);
      if (d * d !== n) out.push(n / d);
    }
  }
  return out;
}

function findRationalRoot(f: Poly): Rational | null {
  if (f.deg < 1) return null;
  const ints = toIntegerCoeffs(f);
  const a0 = ints[0];
  const an = ints[ints.length - 1];
  if (a0 === 0n) return Rational.ZERO;
  for (const p of positiveDivisors(a0)) {
    for (const q of positiveDivisors(an)) {
      for (const s of [1n, -1n]) {
        const r = Rational.make(s * p, q);
        if (f.signAt(r) === 0) return r;
      }
    }
  }
  return null;
}

function extractRationalRoots(f: Poly): { points: Rational[]; rest: Poly } {
  const points: Rational[] = [];
  let rest = f;
  for (;;) {
    const r = findRationalRoot(rest);
    if (r === null) break;
    points.push(r);
    rest = rest.div(Poly.of([r.neg(), Rational.ONE]));
  }
  return { points, rest };
}

function isolateRec(
  seq: Poly[],
  a: Rational,
  b: Rational,
  va: number,
  vb: number,
  out: Interval[],
): void {
  const n = va - vb;
  if (n <= 0) return;
  if (n === 1) {
    out.push({ lo: a, hi: b });
    return;
  }
  const m = a.add(b).half();
  const vm = signVariations(seq, m);
  isolateRec(seq, a, m, va, vm, out);
  isolateRec(seq, m, b, vm, vb, out);
}

/** bisect one step an interval known to contain exactly one root of seq[0] */
function bisectIsolating(seq: Poly[], iv: Interval): Interval {
  const m = iv.lo.add(iv.hi).half();
  const nLeft = signVariations(seq, iv.lo) - signVariations(seq, m);
  return nLeft === 1 ? { lo: iv.lo, hi: m } : { lo: m, hi: iv.hi };
}

/** ensure adjacent isolating intervals are strictly separated (gaps > 0) */
function separate(intervals: Interval[], seq: Poly[]): void {
  for (;;) {
    let touching = -1;
    for (let i = 0; i + 1 < intervals.length; i++) {
      if (intervals[i].hi.cmp(intervals[i + 1].lo) === 0) {
        touching = i;
        break;
      }
    }
    if (touching === -1) return;
    const a = intervals[touching];
    const b = intervals[touching + 1];
    const aPoint = a.lo.equals(a.hi);
    const bPoint = b.lo.equals(b.hi);
    if (!aPoint) intervals[touching] = bisectIsolating(seq, a);
    else if (!bPoint) intervals[touching + 1] = bisectIsolating(seq, b);
    else throw new Error('isolateRoots: duplicate isolating point');
  }
}

/**
 * Isolate all real roots of f into pairwise strictly separated intervals
 * with rational endpoints. Rational roots appear as point intervals [r, r].
 */
export function isolateRoots(f: Poly): Interval[] {
  if (f.deg < 1) return [];
  const sf = f.squareFree();
  const { points, rest } = extractRationalRoots(sf);
  const intervals: Interval[] = points.map((p) => ({ lo: p, hi: p }));
  let seq: Poly[] | null = null;
  if (rest.deg >= 1) {
    seq = sturmSequence(rest);
    const bound = cauchyBound(rest);
    const lo = bound.neg();
    isolateRec(seq, lo, bound, signVariations(seq, lo), signVariations(seq, bound), intervals);
  }
  intervals.sort((u, v) => u.lo.cmp(v.lo));
  if (seq !== null && intervals.length > 1) separate(intervals, seq);
  return intervals;
}

/**
 * Refine a single-root isolating interval of f by exact bisection
 * (used for display; the comparator itself never needs this).
 */
export function tightenInterval(f: Poly, iv: Interval, steps: number): Interval {
  if (iv.lo.equals(iv.hi)) return iv;
  const seq = sturmSequence(f.squareFree());
  let lo = iv.lo;
  let hi = iv.hi;
  for (let i = 0; i < steps; i++) {
    const m = lo.add(hi).half();
    const nLeft = signVariations(seq, lo) - signVariations(seq, m);
    if (nLeft === 1) hi = m;
    else lo = m;
  }
  return { lo, hi };
}
