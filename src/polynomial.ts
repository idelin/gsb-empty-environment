/**
 * Exact polynomial algebra over the rationals (Fraction coefficients,
 * ascending powers). Provides Sturm-sequence root counting so the test
 * suite can prove — not sample — that a derivative numerator has no
 * sign-changing root inside a segment.
 */
import { Fraction } from './fraction.ts';

/** Coefficients in ascending powers: p[i] is the coefficient of x^i. */
export type Poly = Fraction[];

export function trim(p: Poly): Poly {
  let k = p.length;
  while (k > 0 && p[k - 1].isZero()) k--;
  return p.slice(0, k);
}

/** Degree of the polynomial; -1 for the zero polynomial. */
export function degree(p: Poly): number {
  return trim(p).length - 1;
}

export function add(a: Poly, b: Poly): Poly {
  const n = Math.max(a.length, b.length);
  const out: Fraction[] = [];
  for (let i = 0; i < n; i++) {
    const x = i < a.length ? a[i] : Fraction.zero;
    const y = i < b.length ? b[i] : Fraction.zero;
    out.push(x.add(y));
  }
  return trim(out);
}

export function sub(a: Poly, b: Poly): Poly {
  return add(a, b.map((c) => c.neg()));
}

export function mul(a: Poly, b: Poly): Poly {
  if (degree(a) < 0 || degree(b) < 0) return [];
  const out: Fraction[] = [];
  for (let i = 0; i < a.length + b.length - 1; i++) out.push(Fraction.zero);
  for (let i = 0; i < a.length; i++) {
    for (let j = 0; j < b.length; j++) {
      out[i + j] = out[i + j].add(a[i].mul(b[j]));
    }
  }
  return trim(out);
}

export function derivative(p: Poly): Poly {
  const out: Fraction[] = [];
  for (let i = 1; i < p.length; i++) {
    out.push(p[i].mul(Fraction.of(i)));
  }
  return trim(out);
}

export function evaluate(p: Poly, x: Fraction): Fraction {
  let acc = Fraction.zero;
  for (let i = p.length - 1; i >= 0; i--) {
    acc = acc.mul(x).add(p[i]);
  }
  return acc;
}

/** Polynomial long division; returns [quotient, remainder]. */
export function divmod(a: Poly, b: Poly): [Poly, Poly] {
  const db = degree(b);
  if (db < 0) throw new Error('polynomial division by zero');
  let rem = trim(a.slice());
  const quo: Fraction[] = [];
  const leadInv = Fraction.one.div(trim(b)[db]);
  while (degree(rem) >= db) {
    const shift = degree(rem) - db;
    const coeff = trim(rem)[degree(rem)].mul(leadInv);
    while (quo.length <= shift) quo.push(Fraction.zero);
    quo[shift] = coeff;
    const subtrahend: Fraction[] = [];
    for (let i = 0; i < shift; i++) subtrahend.push(Fraction.zero);
    for (let i = 0; i <= db; i++) subtrahend.push(trim(b)[i].mul(coeff));
    rem = sub(rem, subtrahend);
  }
  return [trim(quo), trim(rem)];
}

/** Exact quotient when the divisor is known to divide evenly. */
export function divideExact(a: Poly, b: Poly): Poly {
  const [q, r] = divmod(a, b);
  if (degree(r) >= 0) {
    throw new Error('division is not exact');
  }
  return q;
}

/**
 * Sturm sequence of a non-constant polynomial:
 * p0 = p, p1 = p', p_{k+1} = -rem(p_{k-1}, p_k).
 */
export function sturmSequence(p: Poly): Poly[] {
  const seq: Poly[] = [trim(p)];
  const d = degree(p);
  if (d <= 0) return seq;
  seq.push(derivative(p));
  while (degree(seq[seq.length - 1]) > 0) {
    const [, rem] = divmod(seq[seq.length - 2], seq[seq.length - 1]);
    const neg = trim(rem.map((c) => c.neg()));
    if (degree(neg) < 0) break;
    seq.push(neg);
  }
  return seq;
}

function signVariations(seq: Poly[], x: Fraction): number {
  const signs: number[] = [];
  for (const p of seq) {
    const s = evaluate(p, x).sign();
    if (s !== 0) signs.push(s);
  }
  let variations = 0;
  for (let i = 1; i < signs.length; i++) {
    if (signs[i] !== signs[i - 1]) variations++;
  }
  return variations;
}

/**
 * Exact count of distinct real roots of p in the open interval (a, b),
 * via Sturm's theorem. Roots sitting exactly at a or b are factored out
 * first (and reported through `deflated`), so the count only reflects
 * interior roots.
 */
export function countDistinctRootsInOpenInterval(
  p0: Poly,
  a: Fraction,
  b: Fraction,
): { count: number; deflated: Poly; rootAtA: boolean; rootAtB: boolean } {
  let p = trim(p0);
  let rootAtA = false;
  let rootAtB = false;
  const factorA: Poly = [a.neg(), Fraction.one];
  const factorB: Poly = [b.neg(), Fraction.one];
  while (degree(p) >= 1 && evaluate(p, a).isZero()) {
    p = divideExact(p, factorA);
    rootAtA = true;
  }
  while (degree(p) >= 1 && evaluate(p, b).isZero()) {
    p = divideExact(p, factorB);
    rootAtB = true;
  }
  if (degree(p) <= 0) {
    return { count: 0, deflated: p, rootAtA, rootAtB };
  }
  const seq = sturmSequence(p);
  const count = signVariations(seq, a) - signVariations(seq, b);
  return { count, deflated: p, rootAtA, rootAtB };
}

/** Convert power-basis coefficients to Bernstein coefficients of degree n. */
export function powerToBernstein(p: Poly, n: number): Fraction[] {
  const deg = degree(p);
  if (deg > n) throw new Error('polynomial degree exceeds target Bernstein degree');
  // Elevate to exact degree n in the power basis, then change basis:
  // x^i = sum_{k=i}^{n} C(k,i)/C(n,i) * B_k^n(x).
  const out: Fraction[] = [];
  for (let k = 0; k <= n; k++) out.push(Fraction.zero);
  for (let i = 0; i <= Math.max(deg, 0); i++) {
    const ci = i < p.length ? p[i] : Fraction.zero;
    for (let k = i; k <= n; k++) {
      const ratio = Fraction.of(binomial(k, i)).div(Fraction.of(binomial(n, i)));
      out[k] = out[k].add(ci.mul(ratio));
    }
  }
  return out;
}

function binomial(n: number, k: number): bigint {
  if (k < 0 || k > n) return 0n;
  let result = 1n;
  for (let i = 0; i < k; i++) {
    result = (result * BigInt(n - i)) / BigInt(i + 1);
  }
  return result;
}
