/**
 * Univariate polynomials with exact coefficients and Sturm-theorem root
 * isolation. Coefficient arrays are ascending (index = power of t); the
 * zero polynomial is the empty array.
 */
import { Rat } from "./rational.ts";

export type IntPoly = bigint[];
export type RatPoly = Rat[];

export function normalizeInt(p: IntPoly): IntPoly {
  let n = p.length;
  while (n > 0 && p[n - 1] === 0n) n--;
  return n === p.length ? p : p.slice(0, n);
}

export function normalizeRat(p: RatPoly): RatPoly {
  let n = p.length;
  while (n > 0 && p[n - 1].isZero()) n--;
  return n === p.length ? p : p.slice(0, n);
}

export function degree(p: unknown[]): number {
  return p.length - 1;
}

export function intToRatPoly(p: IntPoly): RatPoly {
  return p.map((c) => Rat.of(c));
}

/** Clears denominators: returns { poly, scale } with poly = scale * p. */
export function ratPolyToIntPoly(p: RatPoly): { poly: IntPoly; scale: Rat } {
  let scale = 1n;
  for (const c of p) {
    scale = (scale / gcdBig(scale, c.den)) * c.den;
  }
  const poly = normalizeInt(p.map((c) => c.num * (scale / c.den)));
  // Reduce by the content so the integer polynomial is primitive.
  let content = 0n;
  for (const c of poly) content = gcdBig(content, c < 0n ? -c : c);
  if (content > 1n) {
    return { poly: poly.map((c) => c / content), scale: Rat.of(scale, content) };
  }
  return { poly, scale: Rat.of(scale) };
}

function gcdBig(a: bigint, b: bigint): bigint {
  if (a < 0n) a = -a;
  if (b < 0n) b = -b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export function addRat(a: RatPoly, b: RatPoly): RatPoly {
  const n = Math.max(a.length, b.length);
  const out: Rat[] = [];
  for (let i = 0; i < n; i++) {
    const x = i < a.length ? a[i] : Rat.of(0n);
    const y = i < b.length ? b[i] : Rat.of(0n);
    out.push(x.add(y));
  }
  return normalizeRat(out);
}

export function negRat(a: RatPoly): RatPoly {
  return a.map((c) => c.neg());
}

export function subRat(a: RatPoly, b: RatPoly): RatPoly {
  return addRat(a, negRat(b));
}

export function mulRat(a: RatPoly, b: RatPoly): RatPoly {
  if (a.length === 0 || b.length === 0) return [];
  const out: Rat[] = Array.from({ length: a.length + b.length - 1 }, () => Rat.of(0n));
  for (let i = 0; i < a.length; i++) {
    for (let j = 0; j < b.length; j++) {
      out[i + j] = out[i + j].add(a[i].mul(b[j]));
    }
  }
  return normalizeRat(out);
}

export function derivRat(a: RatPoly): RatPoly {
  const out: Rat[] = [];
  for (let i = 1; i < a.length; i++) out.push(a[i].mul(BigInt(i)));
  return normalizeRat(out);
}

export function evalRat(p: RatPoly, x: Rat): Rat {
  let acc = Rat.of(0n);
  for (let i = p.length - 1; i >= 0; i--) acc = acc.mul(x).add(p[i]);
  return acc;
}

export function evalInt(p: IntPoly, x: Rat): Rat {
  let acc = Rat.of(0n);
  for (let i = p.length - 1; i >= 0; i--) acc = acc.mul(x).add(Rat.of(p[i]));
  return acc;
}

/** Polynomial long division over the rationals: a = q*b + r, deg r < deg b. */
export function divRemRat(a: RatPoly, b: RatPoly): [RatPoly, RatPoly] {
  if (b.length === 0) throw new Error("poly: division by zero polynomial");
  const rem = a.slice();
  const q: Rat[] = Array.from({ length: Math.max(0, a.length - b.length + 1) }, () =>
    Rat.of(0n),
  );
  while (rem.length >= b.length && rem.length > 0) {
    const shift = rem.length - b.length;
    const factor = rem[rem.length - 1].div(b[b.length - 1]);
    q[shift] = factor;
    for (let i = 0; i < b.length; i++) {
      rem[shift + i] = rem[shift + i].sub(factor.mul(b[i]));
    }
    while (rem.length > 0 && rem[rem.length - 1].isZero()) rem.pop();
  }
  return [normalizeRat(q), normalizeRat(rem)];
}

/**
 * Canonical Sturm sequence: s0 = p, s1 = p', s_{k+1} = -rem(s_{k-1}, s_k).
 * The number of distinct real roots of p in (a, b] equals V(a) - V(b),
 * where V(x) counts sign variations of the sequence at x (zeros skipped).
 */
export function sturmSequence(p: RatPoly): RatPoly[] {
  const seq: RatPoly[] = [normalizeRat(p)];
  if (p.length <= 1) return seq;
  seq.push(derivRat(p));
  while (seq[seq.length - 1].length > 0) {
    const [, rem] = divRemRat(seq[seq.length - 2], seq[seq.length - 1]);
    if (rem.length === 0) break;
    seq.push(negRat(rem));
  }
  return seq;
}

export function signVariations(seq: RatPoly[], x: Rat): number {
  let variations = 0;
  let prevSign = 0;
  for (const s of seq) {
    const sign = evalRat(s, x).sign();
    if (sign === 0) continue;
    if (prevSign !== 0 && sign !== prevSign) variations++;
    prevSign = sign;
  }
  return variations;
}

export interface IsolatedRoot {
  /** Exact rational root when one was hit during bisection, else null. */
  exact: Rat | null;
  /** Certified isolating interval with rational endpoints (lo === hi when exact). */
  lo: Rat;
  hi: Rat;
}

/** Isolates all distinct roots of p in the open interval (a, b). */
export function isolateRoots(p: RatPoly, a: Rat, b: Rat): IsolatedRoot[] {
  const seq = sturmSequence(p);
  const roots: IsolatedRoot[] = [];
  const countHalfOpen = (lo: Rat, hi: Rat): number =>
    signVariations(seq, lo) - signVariations(seq, hi); // roots in (lo, hi]

  const rec = (lo: Rat, hi: Rat, n: number): void => {
    if (n === 0) return;
    if (n === 1) {
      roots.push({ exact: null, lo, hi });
      return;
    }
    const mid = lo.add(hi).div(2);
    if (evalRat(seq[0], mid).isZero()) {
      roots.push({ exact: mid, lo: mid, hi: mid });
      rec(lo, mid, countHalfOpen(lo, mid) - 1); // mid itself already recorded
      rec(mid, hi, countHalfOpen(mid, hi));
    } else {
      const left = countHalfOpen(lo, mid);
      rec(lo, mid, left);
      rec(mid, hi, n - left);
    }
  };

  const atB = evalRat(seq[0], b).isZero() ? 1 : 0;
  rec(a, b, countHalfOpen(a, b) - atB); // exclude a root exactly at b
  roots.sort((x, y) => x.lo.cmp(y.lo));
  return roots;
}

/**
 * Narrows an isolating interval until its width is below 2^-bits (and,
 * when requireInterior, until it lies strictly inside (0, 1)). May
 * discover that the root is an exact rational.
 */
export function refineRoot(
  seq: RatPoly[],
  root: IsolatedRoot,
  bits: number,
  requireInterior = false,
): IsolatedRoot {
  if (root.exact) return root;
  let lo = root.lo;
  let hi = root.hi;
  const p = seq[0];
  let vLo = signVariations(seq, lo);
  const widthOk = (): boolean => {
    if (requireInterior && (lo.sign() === 0 || hi.cmp(1) === 0)) return false;
    // hi - lo < 2^-bits  <=>  (hi-lo).num * 2^bits < (hi-lo).den
    const w = hi.sub(lo);
    return w.num * (1n << BigInt(bits)) < w.den;
  };
  while (!widthOk()) {
    const mid = lo.add(hi).div(2);
    if (evalRat(p, mid).isZero()) return { exact: mid, lo: mid, hi: mid };
    const vMid = signVariations(seq, mid);
    if (vLo - vMid >= 1) {
      hi = mid;
    } else {
      lo = mid;
      vLo = vMid;
    }
  }
  return { exact: null, lo, hi };
}

/** Sum of |coefficients|, a rigorous bound for max |p(t)| on [0, 1]. */
export function l1NormInt(p: IntPoly): bigint {
  let acc = 0n;
  for (const c of p) acc += c < 0n ? -c : c;
  return acc;
}

export function polyToString(p: IntPoly | RatPoly, variable = "t"): string {
  if (p.length === 0) return "0";
  const coeff = (i: number): string =>
    typeof p[i] === "bigint" ? (p[i] as bigint).toString() : (p[i] as Rat).toString();
  const parts: string[] = [];
  for (let i = p.length - 1; i >= 0; i--) {
    const c = coeff(i);
    if (c === "0") continue;
    const neg = c.startsWith("-");
    const abs = neg ? c.slice(1) : c;
    const unit = abs === "1" && i > 0 ? "" : abs;
    const term = i === 0 ? unit : i === 1 ? `${unit}${variable}` : `${unit}${variable}^${i}`;
    parts.push(parts.length === 0 ? (neg ? `-${term}` : term) : neg ? `- ${term}` : `+ ${term}`);
  }
  return parts.length === 0 ? "0" : parts.join(" ");
}
