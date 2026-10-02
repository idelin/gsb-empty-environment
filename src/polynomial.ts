/**
 * Univariate polynomials with exact integer (BigInt) coefficients.
 * coeffs[i] is the coefficient of t^i.
 */
import { Fraction, absBig, gcd } from "./fraction.ts";

export type IntPoly = bigint[];
type RatPoly = Fraction[];

export const ZERO_POLY: IntPoly = [0n];

export function trim(p: IntPoly): IntPoly {
  let n = p.length;
  while (n > 1 && p[n - 1] === 0n) n--;
  return n === p.length ? p : p.slice(0, n);
}

export function degree(p: IntPoly): number { return trim(p).length - 1; }

export function isZeroPoly(p: IntPoly): boolean {
  return p.every((c) => c === 0n);
}

export function add(a: IntPoly, b: IntPoly): IntPoly {
  const n = Math.max(a.length, b.length);
  const r = new Array<bigint>(n).fill(0n);
  for (let i = 0; i < n; i++) r[i] = (a[i] ?? 0n) + (b[i] ?? 0n);
  return trim(r);
}

export function sub(a: IntPoly, b: IntPoly): IntPoly {
  const n = Math.max(a.length, b.length);
  const r = new Array<bigint>(n).fill(0n);
  for (let i = 0; i < n; i++) r[i] = (a[i] ?? 0n) - (b[i] ?? 0n);
  return trim(r);
}

export function mul(a: IntPoly, b: IntPoly): IntPoly {
  if (isZeroPoly(a) || isZeroPoly(b)) return [0n];
  const r = new Array<bigint>(a.length + b.length - 1).fill(0n);
  for (let i = 0; i < a.length; i++)
    if (a[i] !== 0n)
      for (let j = 0; j < b.length; j++)
        if (b[j] !== 0n) r[i + j] += a[i] * b[j];
  return trim(r);
}

export function scale(p: IntPoly, k: bigint): IntPoly {
  return trim(p.map((c) => c * k));
}

export function deriv(p: IntPoly): IntPoly {
  if (p.length <= 1) return [0n];
  const r = new Array<bigint>(p.length - 1);
  for (let i = 1; i < p.length; i++) r[i - 1] = p[i] * BigInt(i);
  return trim(r);
}

/** Exact evaluation at a rational point. */
export function evalAt(p: IntPoly, x: Fraction): Fraction {
  let acc = Fraction.ZERO;
  for (let i = p.length - 1; i >= 0; i--) {
    acc = acc.mul(x).add(new Fraction(p[i]));
  }
  return acc;
}

export function signAt(p: IntPoly, x: Fraction): number {
  return evalAt(p, x).sign();
}

/**
 * Divide by the content (gcd of coefficients) -> primitive polynomial.
 * The overall sign is preserved: callers rely on it for derivative sign tests.
 */
export function primitive(p: IntPoly): IntPoly {
  const t = trim(p);
  let g = 0n;
  for (const c of t) g = gcd(g, absBig(c));
  if (g === 0n) g = 1n;
  return trim(t.map((c) => c / g));
}

/** Exact division by (q*t - p) where p/q is a root. Result keeps integer coeffs. */
export function deflateLinear(p: IntPoly, pRoot: bigint, qRoot: bigint): IntPoly {
  const t = trim(p);
  const n = t.length - 1;
  const d = new Array<bigint>(n).fill(0n);
  // P = (q t - p) Q;  d_{n-1} = a_n / q ;  d_{k-1} = (a_k + p * d_k) / q
  d[n - 1] = t[n] / qRoot;
  for (let k = n - 1; k >= 1; k--) {
    d[k - 1] = (t[k] + pRoot * d[k]) / qRoot;
  }
  // verify constant term exactly cancels
  if (t[0] + pRoot * d[0] !== 0n) throw new Error("deflateLinear: not an exact root");
  return trim(d);
}

/** All rational roots with multiplicities, via the rational root theorem. */
export function rationalRoots(poly: IntPoly): { root: Fraction; multiplicity: number }[] {
  let p = primitive(poly);
  const out: { root: Fraction; multiplicity: number }[] = [];
  if (degree(p) < 1) return out;

  // factor out t^k (root 0)
  let zeros = 0;
  while (p.length > 1 && p[0] === 0n) { p = trim(p.slice(1)); zeros++; }
  if (zeros > 0) out.push({ root: Fraction.ZERO, multiplicity: zeros });

  while (degree(p) >= 1) {
    const a0 = absBig(p[0]);
    const an = absBig(p[p.length - 1]);
    let found: Fraction | null = null;
    // candidates p/q with p | a0, q | an
    const ps = divisors(a0);
    const qs = divisors(an);
    outer: for (const q of qs) {
      for (const pp of ps) {
        for (const s of [1n, -1n]) {
          const cand = new Fraction(s * pp, q);
          if (evalAt(p, cand).isZero) { found = cand; break outer; }
        }
      }
    }
    if (found === null) break;
    out.push({ root: found, multiplicity: 1 });
    p = primitive(deflateLinear(p, found.num, found.den));
    // recount remaining multiplicity of the same root
    while (degree(p) >= 1 && evalAt(p, found).isZero) {
      out[out.length - 1].multiplicity++;
      p = primitive(deflateLinear(p, found.num, found.den));
    }
  }
  return out;
}

/** Remove all rational roots; returns the remaining (irrational-root) factor. */
export function removeRationalRoots(poly: IntPoly): IntPoly {
  let p = primitive(poly);
  for (const { root, multiplicity } of rationalRoots(p)) {
    for (let i = 0; i < multiplicity; i++) {
      p = primitive(deflateLinear(p, root.num, root.den));
    }
  }
  return p;
}

function divisors(n: bigint): bigint[] {
  if (n === 0n) return [1n];
  const out: bigint[] = [];
  for (let d = 1n; d * d <= n; d++) {
    if (n % d === 0n) {
      out.push(d);
      if (d * d !== n) out.push(n / d);
    }
  }
  return out;
}

/* ---------------- Sturm sequences (exact, rational coefficients) ---------------- */

function toRat(p: IntPoly): RatPoly { return trim(p).map((c) => new Fraction(c)); }

function ratTrim(p: RatPoly): RatPoly {
  let n = p.length;
  while (n > 0 && p[n - 1].isZero) n--;
  if (n === 0) return [Fraction.ZERO];
  return n === p.length ? p : p.slice(0, n);
}

function ratDeriv(p: RatPoly): RatPoly {
  if (p.length <= 1) return [Fraction.ZERO];
  const r: Fraction[] = [];
  for (let i = 1; i < p.length; i++) r.push(p[i].mul(new Fraction(BigInt(i))));
  return ratTrim(r);
}

function ratRem(a: RatPoly, b: RatPoly): RatPoly {
  let r = a.slice();
  const db = b.length - 1;
  const lb = b[db];
  while (r.length > 1 || (r.length === 1 && !r[0].isZero)) {
    const dr = r.length - 1;
    if (dr < db) break;
    const f = r[dr].div(lb);
    const shift = dr - db;
    const nr = new Array<Fraction>(dr).fill(Fraction.ZERO);
    for (let i = 0; i < dr; i++) nr[i] = r[i];
    for (let i = 0; i <= db; i++) {
      const idx = i + shift;
      if (idx < dr) nr[idx] = nr[idx].sub(b[i].mul(f));
    }
    r = ratTrim(nr);
    if (r.length === 1 && r[0].isZero) break;
  }
  return r;
}

/** Sturm sequence of p (p must be squarefree-free of rational factors already removed). */
export function sturmSequence(p: IntPoly): RatPoly[] {
  const seq: RatPoly[] = [];
  let s0 = toRat(p);
  let s1 = ratDeriv(s0);
  seq.push(s0, s1);
  while (!(s1.length === 1 && s1[0].isZero)) {
    const rem = ratRem(s0, s1);
    const next = rem.map((c) => c.neg());
    if (next.length === 1 && next[0].isZero) break;
    seq.push(ratTrim(next));
    s0 = s1;
    s1 = ratTrim(next);
  }
  return seq;
}

function evalRat(p: RatPoly, x: Fraction): Fraction {
  let acc = Fraction.ZERO;
  for (let i = p.length - 1; i >= 0; i--) acc = acc.mul(x).add(p[i]);
  return acc;
}

function signVariations(seq: RatPoly[], x: Fraction): number {
  let count = 0;
  let prev = 0;
  for (const s of seq) {
    const sg = evalRat(s, x).sign();
    if (sg === 0) continue;
    if (prev !== 0 && sg !== prev) count++;
    prev = sg;
  }
  return count;
}

/**
 * Number of distinct real roots of p in the open interval (a, b).
 * Requires p(a) != 0 and p(b) != 0 (guaranteed when rational roots were removed
 * and a, b are rational).
 */
export function countRootsIn(seq: RatPoly[], a: Fraction, b: Fraction): number {
  return signVariations(seq, a) - signVariations(seq, b);
}

export interface RootInterval {
  lo: Fraction;
  hi: Fraction;
}

/**
 * Isolate every distinct root of p in (a, b) into disjoint rational intervals
 * of width <= precision. p must have no rational roots in [a, b]
 * (use removeRationalRoots first).
 */
export function isolateRoots(
  p: IntPoly,
  a: Fraction,
  b: Fraction,
  precision: Fraction,
): RootInterval[] {
  if (degree(p) < 1 || isZeroPoly(p)) return [];
  const seq = sturmSequence(p);
  const out: RootInterval[] = [];
  const two = new Fraction(2n);

  const recurse = (lo: Fraction, hi: Fraction): void => {
    const n = countRootsIn(seq, lo, hi);
    if (n === 0) return;
    if (n === 1) {
      // bisect until narrow enough
      let l = lo, h = hi;
      while (h.sub(l).cmp(precision) > 0) {
        const mid = l.add(h).div(two);
        if (countRootsIn(seq, l, mid) === 1) h = mid; else l = mid;
      }
      out.push({ lo: l, hi: h });
      return;
    }
    const mid = lo.add(hi).div(two);
    recurse(lo, mid);
    recurse(mid, hi);
  };

  recurse(a, b);
  return out;
}

/** Pretty-print an integer polynomial in t. */
export function polyToString(p: IntPoly): string {
  const t = trim(p);
  if (t.length === 1 && t[0] === 0n) return "0";
  const terms: string[] = [];
  for (let i = t.length - 1; i >= 0; i--) {
    const c = t[i];
    if (c === 0n) continue;
    const sign = c < 0n ? "-" : "+";
    const mag = absBig(c);
    const coef = mag === 1n && i > 0 ? "" : `${mag}`;
    const varPart = i === 0 ? "" : i === 1 ? "t" : `t^${i}`;
    terms.push(`${sign} ${coef}${coef && varPart ? "*" : ""}${varPart}`);
  }
  const s = terms.join(" ");
  return s.startsWith("+ ") ? s.slice(2) : s;
}
