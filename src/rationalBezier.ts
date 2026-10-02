/**
 * Extrema locator for 1D rational Bezier curves.
 *
 * A curve y(t) = N(t) / D(t) with N(t) = sum w_i p_i B_i(t) and
 * D(t) = sum w_i B_i(t) has derivative y' = (N' D - N D') / D^2.
 * With positive weights D(t) >= min(w_i) > 0 on [0, 1], so the sign of
 * y' is the sign of the polynomial P = N' D - N D'. We export P as a
 * primitive integer-coefficient polynomial, isolate all of its distinct
 * roots in (0, 1) exactly (Sturm's theorem over rational arithmetic),
 * keep the sign-changing ones as extrema, and certify every reported
 * value with exact rational bounds.
 */
import { Rat, type RatInput } from "./rational.ts";
import {
  addRat,
  derivRat,
  evalInt,
  evalRat,
  isolateRoots,
  l1NormInt,
  mulRat,
  polyToString,
  ratPolyToIntPoly,
  refineRoot,
  sturmSequence,
  subRat,
  type IntPoly,
  type IsolatedRoot,
  type RatPoly,
} from "./poly.ts";

export interface RationalBezier1D {
  points: Rat[];
  weights: Rat[];
}

export function createCurve(points: RatInput[], weights: RatInput[]): RationalBezier1D {
  if (points.length !== weights.length) {
    throw new Error(`need as many weights as control points (${points.length} != ${weights.length})`);
  }
  if (points.length < 2) throw new Error("need at least 2 control points");
  const ws = weights.map((w) => Rat.from(w));
  for (const w of ws) {
    if (w.sign() <= 0) throw new Error(`weights must be positive, got ${w}`);
  }
  return { points: points.map((p) => Rat.from(p)), weights: ws };
}

function binomial(n: number, k: number): bigint {
  let acc = 1n;
  for (let i = 0; i < k; i++) {
    acc = (acc * BigInt(n - i)) / BigInt(i + 1);
  }
  return acc;
}

/** Power-basis coefficients of each Bernstein basis polynomial B_i^n. */
export function bernsteinToPowerBasis(degree: number): IntPoly[] {
  const basis: IntPoly[] = [];
  for (let i = 0; i <= degree; i++) {
    const coeffs: bigint[] = new Array<bigint>(degree + 1).fill(0n);
    const ci = binomial(degree, i);
    for (let j = i; j <= degree; j++) {
      const term = ci * binomial(degree - i, j - i);
      coeffs[j] = (j - i) % 2 === 0 ? term : -term;
    }
    basis.push(coeffs);
  }
  return basis;
}

/** Numerator N(t) and denominator D(t) of the curve as exact polynomials. */
export function numeratorDenominator(curve: RationalBezier1D): { N: RatPoly; D: RatPoly } {
  const n = curve.points.length - 1;
  const basis = bernsteinToPowerBasis(n);
  const N: RatPoly = Array.from({ length: n + 1 }, () => Rat.of(0n));
  const D: RatPoly = Array.from({ length: n + 1 }, () => Rat.of(0n));
  for (let i = 0; i <= n; i++) {
    const wi = curve.weights[i];
    const wiPi = wi.mul(curve.points[i]);
    for (let j = 0; j <= n; j++) {
      N[j] = N[j].add(wiPi.mul(basis[i][j]));
      D[j] = D[j].add(wi.mul(basis[i][j]));
    }
  }
  return { N, D };
}

/**
 * The extrema equation: P(t) = N'(t) D(t) - N(t) D'(t), returned as a
 * primitive integer-coefficient polynomial plus the scale factor such
 * that P_int = scale * P_exact.
 */
export function derivativePolynomial(curve: RationalBezier1D): {
  poly: IntPoly;
  scale: Rat;
  exact: RatPoly;
} {
  const { N, D } = numeratorDenominator(curve);
  const exact = subRat(mulRat(derivRat(N), D), mulRat(N, derivRat(D)));
  const { poly, scale } = ratPolyToIntPoly(exact);
  return { poly, scale, exact };
}

/** A parameter location: exact rational, or a certified rational interval. */
export interface TRef {
  exact: Rat | null;
  lo: Rat;
  hi: Rat;
  approx: Rat;
}

export interface CriticalPoint {
  t: TRef;
  kind: "max" | "min";
  /** y at the reported t (exact when t is exact). */
  value: Rat;
  /** Certified bounds on the true extreme value. */
  valueLo: Rat;
  valueHi: Rat;
}

export interface Segment {
  from: TRef;
  to: TRef;
  direction: "increasing" | "decreasing" | "constant";
}

export interface Extremum {
  /** All locations attaining the extremum (more than one on ties). */
  locations: TRef[];
  value: Rat;
  valueLo: Rat;
  valueHi: Rat;
}

export interface AnalysisResult {
  degree: number;
  numeratorPoly: RatPoly;
  denominatorPoly: RatPoly;
  /** Primitive integer polynomial whose roots in [0, 1] are the extrema equation. */
  derivativePoly: IntPoly;
  derivativeScale: Rat;
  derivativePolyString: string;
  /** Endpoints where the derivative vanishes exactly. */
  endpointRoots: Rat[];
  /** Interior derivative roots without a sign change (not extrema). */
  stationaryRoots: TRef[];
  criticalPoints: CriticalPoint[];
  segments: Segment[];
  globalMax: Extremum;
  globalMin: Extremum;
  valueAt(t: RatInput): Rat;
}

export interface AnalyzeOptions {
  /** Isolating intervals are narrowed below 2^-precisionBits (default 48). */
  precisionBits?: number;
}

function toTRef(root: IsolatedRoot): TRef {
  const approx = root.exact ?? root.lo.add(root.hi).div(2);
  return { exact: root.exact, lo: root.lo, hi: root.hi, approx };
}

const EXACT_ZERO: TRef = { exact: Rat.of(0n), lo: Rat.of(0n), hi: Rat.of(0n), approx: Rat.of(0n) };
const EXACT_ONE: TRef = { exact: Rat.of(1n), lo: Rat.of(1n), hi: Rat.of(1n), approx: Rat.of(1n) };

export function analyze(
  points: RatInput[],
  weights: RatInput[],
  options: AnalyzeOptions = {},
): AnalysisResult {
  const precisionBits = options.precisionBits ?? 48;
  const curve = createCurve(points, weights);
  const { N, D } = numeratorDenominator(curve);
  const { poly: P, scale, exact: PExact } = derivativePolynomial(curve);
  const minWeight = curve.weights.reduce((a, b) => Rat.min(a, b));

  const valueAt = (t: RatInput): Rat => {
    const x = Rat.from(t);
    return evalRat(N, x).div(evalRat(D, x));
  };

  // |y'| <= L1(P_int) / (scale * minWeight^2) on [0, 1]: a rigorous bound.
  const derivBound = Rat.of(l1NormInt(P)).div(scale.mul(minWeight).mul(minWeight));

  const endpointRoots: Rat[] = [];
  if (P.length > 0 && evalInt(P, Rat.of(0n)).isZero()) endpointRoots.push(Rat.of(0n));
  if (P.length > 0 && evalInt(P, Rat.of(1n)).isZero()) endpointRoots.push(Rat.of(1n));

  // --- isolate every distinct root of P in the open interval (0, 1) ---
  const isolated = P.length === 0 ? [] : isolateRoots(PExact, Rat.of(0n), Rat.of(1n));
  const seq = P.length === 0 ? [] : sturmSequence(PExact);
  const refined = isolated.map((r) => refineRoot(seq, r, precisionBits, true));
  const roots: TRef[] = refined.map(toTRef);

  // --- sign of the derivative on each root-free band ---
  const samplePoints: Rat[] = [];
  if (roots.length === 0) {
    samplePoints.push(Rat.of(1n, 2n));
  } else {
    for (let i = 0; i <= roots.length; i++) {
      if (i === 0) samplePoints.push(roots[0].lo.div(2));
      else if (i === roots.length) samplePoints.push(roots[roots.length - 1].hi.add(1).div(2));
      else samplePoints.push(roots[i - 1].hi.add(roots[i].lo).div(2));
    }
  }
  const signs = samplePoints.map((s) => evalInt(P, s).sign());

  const tRefs: TRef[] = [EXACT_ZERO, ...roots, EXACT_ONE];
  const segments: Segment[] = [];
  for (let i = 0; i < tRefs.length - 1; i++) {
    const direction = signs[i] > 0 ? "increasing" : signs[i] < 0 ? "decreasing" : "constant";
    segments.push({ from: tRefs[i], to: tRefs[i + 1], direction });
  }

  // --- classify roots: only sign-changing ones are extrema ---
  const criticalPoints: CriticalPoint[] = [];
  const stationaryRoots: TRef[] = [];
  roots.forEach((t, i) => {
    const left = signs[i];
    const right = signs[i + 1];
    if (left === right || left === 0 || right === 0) {
      stationaryRoots.push(t);
      return;
    }
    criticalPoints.push({
      t,
      kind: left > 0 ? "max" : "min",
      ...certifiedValue(t, precisionBits),
    });
  });

  function certifiedValue(t: TRef, bits: number): { value: Rat; valueLo: Rat; valueHi: Rat } {
    if (t.exact) {
      const v = valueAt(t.exact);
      return { value: v, valueLo: v, valueHi: v };
    }
    // Re-isolate at the requested precision, then bound |y(t*) - y(mid)|
    // by derivBound * half-width of the isolating interval.
    const root = refineRoot(seq, { exact: null, lo: t.lo, hi: t.hi }, bits, true);
    if (root.exact) {
      const v = valueAt(root.exact);
      t.exact = root.exact;
      t.lo = root.exact;
      t.hi = root.exact;
      t.approx = root.exact;
      return { value: v, valueLo: v, valueHi: v };
    }
    t.lo = root.lo;
    t.hi = root.hi;
    t.approx = root.lo.add(root.hi).div(2);
    const value = valueAt(t.approx);
    const err = derivBound.mul(root.hi.sub(root.lo)).div(2);
    return { value, valueLo: value.sub(err), valueHi: value.add(err) };
  }

  // --- global extrema over endpoints + interior critical points ---
  interface Candidate {
    t: TRef;
    interval(bits: number): [Rat, Rat];
  }
  const candidates: Candidate[] = [
    { t: EXACT_ZERO, interval: () => { const v = valueAt(0n); return [v, v]; } },
    { t: EXACT_ONE, interval: () => { const v = valueAt(1n); return [v, v]; } },
    ...criticalPoints.map((cp): Candidate => {
      return {
        t: cp.t,
        interval: (bits) => {
          const { valueLo, valueHi } = certifiedValue(cp.t, bits);
          return [valueLo, valueHi];
        },
      };
    }),
  ];

  const selectExtremum = (wantMax: boolean): Extremum => {
    let bits = precisionBits;
    for (let iter = 0; iter < 200; iter++) {
      const ivs = candidates.map((c) => c.interval(bits));
      let best = 0;
      for (let i = 1; i < ivs.length; i++) {
        const better = wantMax ? ivs[i][0].cmp(ivs[best][0]) > 0 : ivs[i][0].cmp(ivs[best][0]) < 0;
        if (better) best = i;
      }
      // Winners: candidates whose interval can still tie or beat the best.
      const winners: number[] = [];
      for (let i = 0; i < ivs.length; i++) {
        const canTie = wantMax
          ? ivs[i][1].cmp(ivs[best][0]) >= 0
          : ivs[i][1].cmp(ivs[best][0]) <= 0;
        if (canTie) winners.push(i);
      }
      const decided =
        winners.length === 1 ||
        winners.every((i) => ivs[i][0].equals(ivs[i][1]) && ivs[i][0].equals(ivs[best][0]));
      if (decided) {
        const valueLo = ivs[best][0];
        const valueHi = winners.reduce((acc, i) => Rat.max(acc, ivs[i][1]), ivs[best][1]);
        const value = winners.every((i) => ivs[i][0].equals(ivs[i][1]))
          ? ivs[best][0]
          : valueLo.add(valueHi).div(2);
        return { locations: winners.map((i) => candidates[i].t), value, valueLo, valueHi };
      }
      bits *= 2;
    }
    throw new Error("could not separate extrema candidates (ill-conditioned curve)");
  };

  return {
    degree: curve.points.length - 1,
    numeratorPoly: N,
    denominatorPoly: D,
    derivativePoly: P,
    derivativeScale: scale,
    derivativePolyString: polyToString(P),
    endpointRoots,
    stationaryRoots,
    criticalPoints,
    segments,
    globalMax: selectExtremum(true),
    globalMin: selectExtremum(false),
    valueAt,
  };
}
