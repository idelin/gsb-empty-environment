/**
 * Rational Bezier curve (1D) extremum locator.
 *
 *   y(t) = N(t) / D(t)
 *   N(t) = sum_i w_i * y_i * B_i^n(t)
 *   D(t) = sum_i w_i       * B_i^n(t)
 *
 * Stationary points satisfy P(t) = N'(t) D(t) - N(t) D'(t) = 0,
 * an integer-coefficient polynomial equation (D(t) > 0 for positive weights).
 * Roots in [0, 1] are found exactly (rational roots) or isolated into
 * certified rational intervals (irrational roots) via Sturm's theorem.
 */
import { Fraction, lcm } from "./fraction.ts";
import {
  type IntPoly, add, deriv, evalAt, isolateRoots, isZeroPoly, mul, polyToString,
  primitive, rationalRoots, removeRationalRoots, scale, signAt, sub, trim,
} from "./polynomial.ts";

export interface CriticalPoint {
  /** exact rational parameter, when the root is rational */
  t: Fraction | null;
  /** certified isolating interval, when the root is irrational */
  interval: { lo: Fraction; hi: Fraction } | null;
  /** multiplicity (known exactly for rational roots; 1 = "distinct" for isolated) */
  multiplicity: number;
  signChange: boolean;
  kind: "max" | "min" | "stationary";
  /** exact curve value when t is rational */
  value: Fraction | null;
  valueApprox: number;
  tApprox: number;
}

export interface MonotonicSegment {
  from: string;
  to: string;
  direction: "increasing" | "decreasing";
}

export interface GlobalExtremum {
  t: string;
  value: Fraction | null; // exact when the argextremum is rational
  valueApprox: number;
}

export interface Analysis {
  degree: number;
  numerator: IntPoly;
  denominator: IntPoly;
  /** primitive integer polynomial P = N'D - ND' whose roots are stationary points */
  derivativePolynomial: IntPoly;
  derivativePolynomialString: string;
  constant: boolean;
  criticalPoints: CriticalPoint[];
  monotonicity: MonotonicSegment[];
  globalMax: GlobalExtremum | null;
  globalMin: GlobalExtremum | null;
}

function binomial(n: number, k: number): bigint {
  let r = 1n;
  for (let i = 0; i < k; i++) r = (r * BigInt(n - i)) / BigInt(i + 1);
  return r;
}

/** Bernstein basis B_i^n(t) as an integer polynomial. */
function bernstein(n: number, i: number): IntPoly {
  // C(n,i) * t^i * (1 - t)^(n-i)
  let p: IntPoly = [1n];
  for (let j = 0; j < n - i; j++) p = mul(p, [1n, -1n] as IntPoly);
  const shifted: IntPoly = new Array<bigint>(i).fill(0n).concat(p);
  return scale(shifted, binomial(n, i));
}

/** Build N(t) and D(t) as integer polynomials (fractions cleared by a common denominator). */
export function buildNumeratorDenominator(
  ys: Fraction[],
  ws: Fraction[],
): { numerator: IntPoly; denominator: IntPoly } {
  const n = ys.length - 1;
  if (n < 1) throw new Error("need at least 2 control points");
  if (ws.length !== ys.length) throw new Error("weights/control points length mismatch");

  // exact rational coefficients first
  const Nf: Fraction[][] = [];
  const Df: Fraction[][] = [];
  let L = 1n;
  for (let i = 0; i <= n; i++) {
    const B = bernstein(n, i);
    const nCoeffs = B.map((b) => ws[i].mul(ys[i]).mul(new Fraction(b)));
    const dCoeffs = B.map((b) => ws[i].mul(new Fraction(b)));
    Nf.push(nCoeffs);
    Df.push(dCoeffs);
    for (const c of nCoeffs) L = lcm(L, c.den);
    for (const c of dCoeffs) L = lcm(L, c.den);
  }
  // clear denominators with ONE common factor so the ratio N/D is preserved
  const N: IntPoly = new Array<bigint>(n + 1).fill(0n);
  const D: IntPoly = new Array<bigint>(n + 1).fill(0n);
  for (let i = 0; i <= n; i++) {
    for (let k = 0; k <= n; k++) {
      N[k] += Nf[i][k].num * (L / Nf[i][k].den);
      D[k] += Df[i][k].num * (L / Df[i][k].den);
    }
  }
  return { numerator: trim(N), denominator: trim(D) };
}

/** P(t) = N'(t) D(t) - N(t) D'(t), as a primitive integer polynomial. */
export function derivativePolynomial(ys: Fraction[], ws: Fraction[]): IntPoly {
  const { numerator: N, denominator: D } = buildNumeratorDenominator(ys, ws);
  return primitive(sub(mul(deriv(N), D), mul(N, deriv(D))));
}

function fmtBoundary(exact: Fraction | null, interval: { lo: Fraction; hi: Fraction } | null): string {
  if (exact !== null) return exact.toString();
  if (interval) return `(${interval.lo} , ${interval.hi})`;
  return "?";
}

export function analyze(
  ys: Fraction[],
  ws: Fraction[],
  precision: Fraction = new Fraction(1n, 1n << 50n),
): Analysis {
  const { numerator: N, denominator: D } = buildNumeratorDenominator(ys, ws);
  const P = derivativePolynomial(ys, ws);
  const valueAt = (t: Fraction): Fraction => evalAt(N, t).div(evalAt(D, t));

  if (isZeroPoly(P)) {
    const c = valueAt(Fraction.ZERO);
    return {
      degree: ys.length - 1,
      numerator: N, denominator: D,
      derivativePolynomial: P,
      derivativePolynomialString: "0",
      constant: true,
      criticalPoints: [],
      monotonicity: [{ from: "0", to: "1", direction: "increasing" }],
      globalMax: { t: "[0,1] (constant)", value: c, valueApprox: c.toNumber() },
      globalMin: { t: "[0,1] (constant)", value: c, valueApprox: c.toNumber() },
    };
  }

  // ---- step 1: exact rational roots (with multiplicities) -------------------
  const ratRoots = rationalRoots(P).filter(
    ({ root }) => root.cmp(Fraction.ZERO) >= 0 && root.cmp(Fraction.ONE) <= 0,
  );

  // ---- step 2: irrational roots isolated by Sturm's theorem -----------------
  const irrationalFactor = removeRationalRoots(P);
  const intervals = isolateRoots(irrationalFactor, Fraction.ZERO, Fraction.ONE, precision);

  // ---- step 3: collect + sort critical locations ----------------------------
  interface Loc {
    exact: Fraction | null;
    interval: { lo: Fraction; hi: Fraction } | null;
    multiplicity: number;
    sortKey: number;
  }
  const locs: Loc[] = [
    ...ratRoots.map(({ root, multiplicity }): Loc => ({
      exact: root, interval: null, multiplicity, sortKey: root.toNumber(),
    })),
    ...intervals.map((iv): Loc => ({
      exact: null, interval: iv, multiplicity: 1,
      sortKey: (iv.lo.toNumber() + iv.hi.toNumber()) / 2,
    })),
  ].sort((a, b) => a.sortKey - b.sortKey);

  // ---- step 4: monotonicity segments between consecutive critical points ----
  const bounds: { exact: Fraction | null; interval: { lo: Fraction; hi: Fraction } | null }[] = [
    { exact: Fraction.ZERO, interval: null },
    ...locs.map((l) => ({ exact: l.exact, interval: l.interval })),
    { exact: Fraction.ONE, interval: null },
  ];
  const two = new Fraction(2n);
  // directions[i] = direction of segment between bounds[i] and bounds[i+1];
  // null for degenerate (zero-width) segments, e.g. a root sitting at t=0 or t=1.
  const directions: ("increasing" | "decreasing" | null)[] = [];
  const monotonicity: MonotonicSegment[] = [];
  for (let i = 0; i < bounds.length - 1; i++) {
    const L = bounds[i], R = bounds[i + 1];
    const lo = L.exact ?? L.interval!.hi;
    const hi = R.exact ?? R.interval!.lo;
    if (lo.cmp(hi) >= 0) { directions.push(null); continue; }
    const mid = lo.add(hi).div(two);
    const dir = signAt(P, mid) >= 0 ? "increasing" : "decreasing";
    directions.push(dir);
    monotonicity.push({
      from: fmtBoundary(L.exact, L.interval),
      to: fmtBoundary(R.exact, R.interval),
      direction: dir,
    });
  }

  // ---- step 5: classify each critical point from adjacent segment directions -
  // locs[i] sits between segment i (left) and segment i+1 (right).
  const criticalPoints: CriticalPoint[] = locs.map((loc, i) => {
    const left = directions[i] ?? null;
    const right = directions[i + 1] ?? null;
    const signChange = left !== null && right !== null && left !== right;
    let kind: CriticalPoint["kind"] = "stationary";
    if (signChange) kind = left === "increasing" ? "max" : "min";

    const tApprox = loc.exact !== null
      ? loc.exact.toNumber()
      : (loc.interval!.lo.toNumber() + loc.interval!.hi.toNumber()) / 2;
    let value: Fraction | null = null;
    let valueApprox: number;
    if (loc.exact !== null) {
      value = valueAt(loc.exact);
      valueApprox = value.toNumber();
    } else {
      const mid = loc.interval!.lo.add(loc.interval!.hi).div(two);
      valueApprox = valueAt(mid).toNumber();
    }
    return {
      t: loc.exact, interval: loc.interval, multiplicity: loc.multiplicity,
      signChange, kind, value, valueApprox, tApprox,
    };
  });

  // ---- step 6: global extrema over endpoints + local extrema ----------------
  interface Cand { tLabel: string; value: Fraction | null; approx: number }
  const v0 = valueAt(Fraction.ZERO);
  const v1 = valueAt(Fraction.ONE);
  const cands: Cand[] = [
    { tLabel: "0", value: v0, approx: v0.toNumber() },
    { tLabel: "1", value: v1, approx: v1.toNumber() },
  ];
  for (const c of criticalPoints) {
    if (c.kind === "stationary") continue;
    cands.push({
      tLabel: fmtBoundary(c.t, c.interval),
      value: c.value,
      approx: c.valueApprox,
    });
  }
  const pick = (wantMax: boolean): Cand => {
    let b = cands[0];
    for (const c of cands) {
      if (wantMax ? c.approx > b.approx : c.approx < b.approx) b = c;
    }
    return b;
  };
  const gMax = pick(true);
  const gMin = pick(false);

  return {
    degree: ys.length - 1,
    numerator: N,
    denominator: D,
    derivativePolynomial: P,
    derivativePolynomialString: polyToString(P),
    constant: false,
    criticalPoints,
    monotonicity,
    globalMax: { t: gMax.tLabel, value: gMax.value, valueApprox: gMax.approx },
    globalMin: { t: gMin.tLabel, value: gMin.value, valueApprox: gMin.approx },
  };
}
