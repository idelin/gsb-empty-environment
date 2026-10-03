import { Rational, gcd, lcm } from "./rational.ts";
import { barycenter, barycentricInHull, convexHull, hullIntersection, hullsClosest } from "./geometry.ts";
import type { Point, BarycentricCertificate } from "./geometry.ts";

export interface Hyperplane {
  /** Coefficients of a*x + b*y + c = 0 (exact rationals). */
  readonly a: Rational;
  readonly b: Rational;
  readonly c: Rational;
  /** Human-readable equation, e.g. "2x - 3y + 5 = 0". */
  readonly equation: string;
}

export interface SeparableResult {
  readonly separable: true;
  readonly hyperplane: Hyperplane;
  /** Closest points between the two hulls; the hyperplane bisects them. */
  readonly witnessA: Point;
  readonly witnessB: Point;
  /** min_{p in A} h(p) > 0 and max_{q in B} h(q) < 0 for h = a*x+b*y+c. */
  readonly minSideA: Rational;
  readonly maxSideB: Rational;
}

export interface NonSeparableResult {
  readonly separable: false;
  /** Vertices of the overlap region conv(A) ∩ conv(B). */
  readonly overlap: Point[];
  /** Barycenter of the overlap region: a point lying in conv(A) ∩ conv(B). */
  readonly point: Point;
  /** Barycentric proof that `point` lies in conv(A). */
  readonly certificateA: BarycentricCertificate;
  /** Barycentric proof that `point` lies in conv(B). */
  readonly certificateB: BarycentricCertificate;
}

export type SeparationResult = SeparableResult | NonSeparableResult;

/**
 * Decides strict linear separability of two finite planar point sets with
 * exact rational arithmetic.
 *
 * - If conv(A) and conv(B) are disjoint (positive distance), returns the
 *   maximum-margin separating hyperplane: the perpendicular bisector of the
 *   closest pair of points between the hulls, with exact rational
 *   coefficients.
 * - Otherwise returns a common point of the two convex hulls together with
 *   barycentric certificates proving membership in both hulls.
 */
export function verifySeparability(A: readonly Point[], B: readonly Point[]): SeparationResult {
  if (A.length === 0 || B.length === 0) throw new Error("point sets must be non-empty");
  const hullA = convexHull(A);
  const hullB = convexHull(B);
  // Two compact convex sets are strictly separable iff they are disjoint.
  const overlap = hullIntersection(hullA, hullB);

  if (overlap.length === 0) {
    // Disjoint hulls have positive distance; the perpendicular bisector of
    // their closest pair of points is a maximum-margin separator.
    const closest = hullsClosest(hullA, hullB);
    // Normal direction: from B's closest point towards A's closest point.
    const a = closest.onFirst.x.sub(closest.onSecond.x);
    const b = closest.onFirst.y.sub(closest.onSecond.y);
    // Hyperplane through the midpoint, perpendicular to the closest-pair segment.
    const mid = {
      x: closest.onFirst.x.add(closest.onSecond.x).div(Rational.of(2)),
      y: closest.onFirst.y.add(closest.onSecond.y).div(Rational.of(2)),
    };
    const c = a.mul(mid.x).add(b.mul(mid.y)).neg();
    const hyperplane: Hyperplane = { a, b, c, equation: formatEquation(a, b, c) };
    const minSideA = minSide(A, hyperplane);
    const maxSideB = maxSide(B, hyperplane);
    if (minSideA.sign() <= 0 || maxSideB.sign() >= 0) {
      throw new Error("internal error: constructed hyperplane does not separate");
    }
    return {
      separable: true,
      hyperplane,
      witnessA: closest.onFirst,
      witnessB: closest.onSecond,
      minSideA,
      maxSideB,
    };
  }

  // Barycenter of the overlap region: a barycentric point of conv(A) ∩ conv(B),
  // certified with exact convex combinations in each hull.
  const common = barycenter(overlap);
  return {
    separable: false,
    overlap,
    point: common,
    certificateA: barycentricInHull(common, hullA),
    certificateB: barycentricInHull(common, hullB),
  };
}

export function sideValue(p: Point, h: Hyperplane): Rational {
  return h.a.mul(p.x).add(h.b.mul(p.y)).add(h.c);
}

function minSide(points: readonly Point[], h: Hyperplane): Rational {
  let m: Rational | null = null;
  for (const p of points) {
    const v = sideValue(p, h);
    if (m === null || v.cmp(m) < 0) m = v;
  }
  return m!;
}

function maxSide(points: readonly Point[], h: Hyperplane): Rational {
  let m: Rational | null = null;
  for (const p of points) {
    const v = sideValue(p, h);
    if (m === null || v.cmp(m) > 0) m = v;
  }
  return m!;
}

/** Scales (a, b, c) to coprime integers and renders "2x - 3y + 5 = 0". */
export function formatEquation(a: Rational, b: Rational, c: Rational): string {
  const denLcm = lcm(lcm(a.den, b.den), c.den);
  let ia = a.num * (denLcm / a.den);
  let ib = b.num * (denLcm / b.den);
  let ic = c.num * (denLcm / c.den);
  const g = gcd(gcd(ia < 0n ? -ia : ia, ib < 0n ? -ib : ib), ic < 0n ? -ic : ic);
  ia /= g; ib /= g; ic /= g;
  if (ia < 0n || (ia === 0n && ib < 0n) || (ia === 0n && ib === 0n && ic < 0n)) {
    ia = -ia; ib = -ib; ic = -ic;
  }

  const terms: string[] = [];
  const pushTerm = (coeff: bigint, variable: string): void => {
    if (coeff === 0n) return;
    const abs = coeff < 0n ? -coeff : coeff;
    const magnitude = variable !== "" && abs === 1n ? "" : abs.toString();
    if (terms.length === 0) {
      terms.push((coeff < 0n ? "-" : "") + magnitude + variable);
    } else {
      terms.push((coeff < 0n ? "- " : "+ ") + magnitude + variable);
    }
  };
  pushTerm(ia, "x");
  pushTerm(ib, "y");
  pushTerm(ic, "");
  if (terms.length === 0) terms.push("0");
  return terms.join(" ") + " = 0";
}

/** Verifies a barycentric certificate: weights in [0,1], sum 1, combine to p. */
export function checkCertificate(p: Point, cert: BarycentricCertificate): boolean {
  let sum = Rational.ZERO;
  let px = Rational.ZERO;
  let py = Rational.ZERO;
  for (let i = 0; i < cert.points.length; i++) {
    const w = cert.weights[i];
    if (w.sign() < 0 || w.cmp(Rational.ONE) > 0) return false;
    sum = sum.add(w);
    px = px.add(w.mul(cert.points[i].x));
    py = py.add(w.mul(cert.points[i].y));
  }
  return sum.eq(Rational.ONE) && px.eq(p.x) && py.eq(p.y);
}
