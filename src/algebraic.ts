/**
 * Algebraic numbers as expression trees. Every node carries:
 *   - an annihilating polynomial over Q (built exactly via resultants)
 *   - an interval with rational endpoints that always encloses the value
 *     and shrinks towards it on refine()
 *
 * Comparison strategy (no floating point anywhere):
 *   1. Compute the minimal polynomial of each side: square-free part of the
 *      annihilating polynomial -> Kronecker irreducible factorization ->
 *      pick the unique factor having a root inside the value's interval
 *      (root counts via Sturm sequences).
 *   2. If the minimal polynomials differ, the numbers differ; refine both
 *      intervals until they separate.
 *   3. If they coincide, isolate all real roots of the common minimal
 *      polynomial into pairwise separated intervals and refine both numbers
 *      until each falls into exactly one isolating interval. Same interval
 *      => equal (e.g. sqrt(2)+sqrt(3) === sqrt(5+2*sqrt(6))).
 */
import { Rational, isqrt } from './rational.ts';
import {
  Poly,
  isolateRoots,
  sturmSequence,
  signVariations,
  type Interval,
} from './poly.ts';
import { factorSquareFree } from './factor.ts';
import { polyAdd, polyMul } from './resultant.ts';

export type Sign = -1 | 0 | 1;

const MAX_REFINEMENTS = 100_000;

const sturmCache = new Map<Poly, Poly[]>();
function sturmOf(f: Poly): Poly[] {
  let s = sturmCache.get(f);
  if (!s) {
    s = sturmSequence(f);
    sturmCache.set(f, s);
  }
  return s;
}

/** number of roots of square-free f in the closed interval [lo, hi] */
function rootCountIn(f: Poly, lo: Rational, hi: Rational): number {
  const seq = sturmOf(f);
  let n = signVariations(seq, lo) - signVariations(seq, hi);
  if (f.signAt(lo) === 0) n += 1;
  return n;
}

/** rational lower/upper bounds for sqrt(r), r >= 0 rational */
function sqrtFloor(r: Rational): Rational {
  const s = isqrt(r.num * r.den);
  return Rational.make(s, r.den);
}
function sqrtCeil(r: Rational): Rational {
  const s = isqrt(r.num * r.den);
  return Rational.make(s * s === r.num * r.den ? s : s + 1n, r.den);
}

export abstract class Alg {
  /** annihilating polynomial (nonzero); the value is one of its roots */
  abstract readonly poly: Poly;
  /** current enclosing interval, always contains the value */
  abstract lo: Rational;
  abstract hi: Rational;
  /** shrink the enclosing interval towards the exact value */
  abstract refine(): void;

  private minpolyCache?: Poly;

  static rational(value: number | bigint | string | Rational): Alg {
    return new RatAlg(Rational.of(value));
  }
  static sqrt(x: Alg): Alg {
    return new SqrtAlg(x);
  }

  add(o: Alg): Alg {
    return new AddAlg(this, o);
  }
  sub(o: Alg): Alg {
    return new AddAlg(this, new NegAlg(o));
  }
  mul(o: Alg): Alg {
    return new MulAlg(this, o);
  }
  div(o: Alg): Alg {
    return new MulAlg(this, new InvAlg(o));
  }
  neg(): Alg {
    return new NegAlg(this);
  }

  isExact(): boolean {
    return this.lo.equals(this.hi);
  }

  /** irreducible monic minimal polynomial over Q */
  minimalPolynomial(): Poly {
    if (this.minpolyCache !== undefined) return this.minpolyCache;
    const factors = factorSquareFree(this.poly.squareFree());
    if (factors.length === 0) throw new Error('minimalPolynomial: no factors');
    if (factors.length === 1) return (this.minpolyCache = factors[0]);
    let guard = 0;
    for (;;) {
      const matches = factors.filter((f) => rootCountIn(f, this.lo, this.hi) > 0);
      if (matches.length === 1) return (this.minpolyCache = matches[0]);
      if (++guard > MAX_REFINEMENTS) throw new Error('minimalPolynomial: no convergence');
      this.refine();
    }
  }

  /** exact comparison: -1 (this < o), 0 (equal), 1 (this > o) */
  compare(o: Alg): Sign {
    const ma = this.minimalPolynomial();
    const mb = o.minimalPolynomial();
    let guard = 0;
    if (ma.equals(mb)) {
      // Same minimal polynomial: equal iff both are the same real root,
      // i.e. fall into the same isolating interval.
      const intervals = isolateRoots(ma);
      for (;;) {
        const ia = intervals.filter(
          (iv) => iv.lo.cmp(this.hi) <= 0 && this.lo.cmp(iv.hi) <= 0,
        );
        const ib = intervals.filter((iv) => iv.lo.cmp(o.hi) <= 0 && o.lo.cmp(iv.hi) <= 0);
        if (ia.length === 1 && ib.length === 1) {
          if (ia[0] === ib[0]) return 0;
          return ia[0].hi.cmp(ib[0].lo) < 0 ? -1 : 1;
        }
        if (++guard > MAX_REFINEMENTS) throw new Error('compare: no convergence');
        this.refine();
        o.refine();
      }
    }
    // Different minimal polynomials => the values differ; separate the intervals.
    for (;;) {
      if (this.hi.cmp(o.lo) < 0) return -1;
      if (o.hi.cmp(this.lo) < 0) return 1;
      if (++guard > MAX_REFINEMENTS) throw new Error('compare: no convergence');
      this.refine();
      o.refine();
    }
  }

  sign(): Sign {
    return this.compare(new RatAlg(Rational.ZERO));
  }
  isZero(): boolean {
    return this.minimalPolynomial().equals(Poly.of([0, 1]));
  }
  equals(o: Alg): boolean {
    return this.compare(o) === 0;
  }
  lessThan(o: Alg): boolean {
    return this.compare(o) < 0;
  }
  greaterThan(o: Alg): boolean {
    return this.compare(o) > 0;
  }

  /** exact truncated decimal of the enclosing interval's midpoint (display only) */
  approxDecimal(digits = 15): string {
    const target = Rational.make(1n, 10n ** BigInt(digits + 1));
    let guard = 0;
    while (this.hi.sub(this.lo).cmp(target) > 0) {
      if (++guard > MAX_REFINEMENTS) throw new Error('approxDecimal: no convergence');
      this.refine();
    }
    return this.lo.add(this.hi).half().toDecimal(digits);
  }

  toString(): string {
    return `Alg(${this.lo.toDecimal(6)} .. ${this.hi.toDecimal(6)})`;
  }
}

/* ------------------------------ nodes ------------------------------ */

class RatAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  constructor(r: Rational) {
    super();
    this.poly = Poly.of([r.neg(), Rational.ONE]);
    this.lo = r;
    this.hi = r;
  }
  refine(): void {
    /* already exact */
  }
}

class SqrtAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  child: Alg;
  constructor(child: Alg) {
    super();
    while (child.lo.sign() < 0) {
      if (child.hi.sign() < 0) throw new Error('sqrt of a negative number');
      child.refine();
    }
    this.child = child;
    this.poly = child.poly.composeX2();
    this.lo = sqrtFloor(child.lo);
    this.hi = sqrtCeil(child.hi);
  }
  refine(): void {
    this.child.refine();
    const loB = sqrtFloor(this.child.lo);
    const hiB = sqrtCeil(this.child.hi);
    if (loB.cmp(this.lo) > 0) this.lo = loB;
    if (hiB.cmp(this.hi) < 0) this.hi = hiB;
    const mid = this.lo.add(this.hi).half();
    const m2 = mid.mul(mid);
    if (this.child.isExact()) {
      const c = m2.cmp(this.child.lo);
      if (c < 0) this.lo = mid;
      else if (c > 0) this.hi = mid;
      else {
        this.lo = mid;
        this.hi = mid;
      }
    } else {
      if (m2.cmp(this.child.lo) < 0) this.lo = mid;
      else if (m2.cmp(this.child.hi) > 0) this.hi = mid;
    }
  }
}

class AddAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  a: Alg;
  b: Alg;
  constructor(a: Alg, b: Alg) {
    super();
    this.a = a;
    this.b = b;
    this.poly = polyAdd(a.poly, b.poly);
    this.lo = a.lo.add(b.lo);
    this.hi = a.hi.add(b.hi);
  }
  refine(): void {
    this.a.refine();
    this.b.refine();
    this.lo = this.a.lo.add(this.b.lo);
    this.hi = this.a.hi.add(this.b.hi);
  }
}

class NegAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  child: Alg;
  constructor(child: Alg) {
    super();
    this.child = child;
    this.poly = child.poly.negateX();
    this.lo = child.hi.neg();
    this.hi = child.lo.neg();
  }
  refine(): void {
    this.child.refine();
    this.lo = this.child.hi.neg();
    this.hi = this.child.lo.neg();
  }
}

class MulAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  a: Alg;
  b: Alg;
  constructor(a: Alg, b: Alg) {
    super();
    this.a = a;
    this.b = b;
    this.poly = polyMul(a.poly, b.poly);
    [this.lo, this.hi] = MulAlg.interval(a, b);
  }
  private static interval(a: Alg, b: Alg): [Rational, Rational] {
    const ps = [a.lo.mul(b.lo), a.lo.mul(b.hi), a.hi.mul(b.lo), a.hi.mul(b.hi)];
    let lo = ps[0];
    let hi = ps[0];
    for (const p of ps) {
      if (p.cmp(lo) < 0) lo = p;
      if (p.cmp(hi) > 0) hi = p;
    }
    return [lo, hi];
  }
  refine(): void {
    this.a.refine();
    this.b.refine();
    [this.lo, this.hi] = MulAlg.interval(this.a, this.b);
  }
}

class InvAlg extends Alg {
  readonly poly: Poly;
  lo: Rational;
  hi: Rational;
  child: Alg;
  constructor(child: Alg) {
    super();
    if (child.sign() === 0) throw new Error('division by zero');
    while (child.lo.sign() <= 0 && child.hi.sign() >= 0) child.refine();
    this.child = child;
    this.poly = child.poly.reciprocal();
    [this.lo, this.hi] = InvAlg.interval(child);
  }
  private static interval(a: Alg): [Rational, Rational] {
    if (a.lo.sign() > 0) return [a.hi.inv(), a.lo.inv()];
    return [a.lo.inv(), a.hi.inv()];
  }
  refine(): void {
    this.child.refine();
    [this.lo, this.hi] = InvAlg.interval(this.child);
  }
}
