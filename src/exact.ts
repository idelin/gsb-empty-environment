/**
 * Exact (symbolic) verification of the per-segment monotonicity claim.
 *
 * With the library's shape parameter r = 1 + alpha + beta, the derivative
 * of a segment satisfies
 *
 *   dP/dx = delta * M(theta) / D(theta)^2,
 *   M(theta) = alpha*(1-theta)^2 + 2*theta*(1-theta) + beta*theta^2,
 *   D(theta) = 1 + (r-3)*theta*(1-theta),
 *
 * i.e. the derivative numerator M is exactly a QUADRATIC whose Bernstein
 * coefficients are [alpha, 1, beta] (scaled by y1 - y0).
 *
 * This module rebuilds that certificate polynomial from the emitted
 * certificate values (alpha, beta, r, y0, y1) with exact rational
 * arithmetic — every double is converted to its exact binary rational
 * value, so the analysis is free of rounding and involves no sampling:
 *
 *  1. Bernstein coefficients of M: all >= 0 proves M >= 0 on the whole
 *     interval [0, 1] (and M > 0 on (0, 1) whenever the segment is not
 *     constant), hence dP/dx keeps the sign of the secant everywhere.
 *  2. The exact discriminant of the quadratic M (identically
 *     (y1-y0)^2 * 4 * (1 - alpha*beta)) gives the classical exact root
 *     analysis.
 *  3. A Sturm sequence independently counts the distinct real roots of
 *     M in the open interval (0, 1); zero interior roots confirms the
 *     absence of sign changes.
 *
 * The certificate describes the designed rational function; the
 * floating-point evaluator reproduces it to within rounding (the test
 * suite checks the emitted power-basis coefficients against this
 * certificate with a tight relative tolerance).
 */
import { Fraction } from './fraction.ts';
import {
  countDistinctRootsInOpenInterval,
  degree,
  powerToBernstein,
  trim,
  type Poly,
} from './polynomial.ts';
import type { SplineSegment } from './rationalCubic.ts';

export interface QuadraticRootAnalysis {
  /** Exact discriminant b^2 - 4ac of the quadratic M(theta). */
  discriminant: Fraction;
  /** 0, 1 (tangent) or 2 distinct real roots over the whole real line. */
  distinctRealRoots: 0 | 1 | 2;
}

export interface ExactSegmentReport {
  segmentIndex: number;
  kind: 'constant' | 'rational';
  /** Power-basis coefficients of the certificate polynomial M(theta). */
  derivativeNumerator: Fraction[];
  /** Bernstein coefficients [c0, c1, c2] = (y1-y0) * [alpha, 1, beta]. */
  bernstein: Fraction[];
  /** True when every Bernstein coefficient has the sign of the secant. */
  bernsteinSignConsistent: boolean;
  /** Bernstein coefficients [1, (r-1)/2, 1] of D(theta). */
  denominatorBernstein: Fraction[];
  denominatorPositive: boolean;
  /** Exact count of distinct real roots of M in the open interval (0, 1). */
  interiorRootCount: number;
  /** Whether M vanishes exactly at theta = 0 / theta = 1. */
  derivativeZeroAtStart: boolean;
  derivativeZeroAtEnd: boolean;
  /** Exact discriminant analysis of the quadratic derivative numerator. */
  quadratic: QuadraticRootAnalysis | null;
  /** Overall exact certificate: Bernstein signs + denominator + Sturm agree. */
  certified: boolean;
}

export function analyzeSegmentExactly(
  segment: SplineSegment,
  segmentIndex: number,
): ExactSegmentReport {
  const c = segment.coeffs;
  const dy = Fraction.fromNumber(c.y1).sub(Fraction.fromNumber(c.y0));
  const alpha = Fraction.fromNumber(c.alpha);
  const beta = Fraction.fromNumber(c.beta);
  const r = Fraction.fromNumber(c.r);

  // D(theta) = 1 + (r-3) t (1-t), exact from the emitted r.
  const den: Poly = trim([
    Fraction.one,
    r.sub(Fraction.of(3)),
    r.sub(Fraction.of(3)).neg(),
  ]);
  const denominatorBernstein = powerToBernstein(den, 2);
  const denominatorPositive =
    denominatorBernstein.length > 0 &&
    denominatorBernstein.every((b) => b.sign() >= 0) &&
    denominatorBernstein.some((b) => b.sign() > 0);

  const secantSign = dy.sign();

  if (secantSign === 0) {
    return {
      segmentIndex,
      kind: 'constant',
      derivativeNumerator: [],
      bernstein: [],
      bernsteinSignConsistent: true,
      denominatorBernstein,
      denominatorPositive,
      interiorRootCount: 0,
      derivativeZeroAtStart: true,
      derivativeZeroAtEnd: true,
      quadratic: null,
      certified: denominatorPositive,
    };
  }

  // M(theta) = dy * [alpha (1-t)^2 + 2 t (1-t) + beta t^2], exact.
  const m: Poly = trim([
    dy.mul(alpha),
    dy.mul(Fraction.of(2)).sub(dy.mul(alpha).mul(Fraction.of(2))),
    dy.mul(alpha).sub(dy.mul(Fraction.of(2))).add(dy.mul(beta)),
  ]);
  const bernstein = powerToBernstein(m, 2);
  const bernsteinSignConsistent = bernstein.every(
    (b) => b.sign() * secantSign >= 0,
  );

  const roots = countDistinctRootsInOpenInterval(m, Fraction.zero, Fraction.one);

  let quadratic: QuadraticRootAnalysis | null = null;
  if (degree(m) === 2) {
    const a = m[2];
    const b = m[1];
    const cc = m[0];
    const disc = b.mul(b).sub(a.mul(cc).mul(Fraction.of(4)));
    const s = disc.sign();
    quadratic = {
      discriminant: disc,
      distinctRealRoots: s > 0 ? 2 : s === 0 ? 1 : 0,
    };
  }

  return {
    segmentIndex,
    kind: 'rational',
    derivativeNumerator: m,
    bernstein,
    bernsteinSignConsistent,
    denominatorBernstein,
    denominatorPositive,
    interiorRootCount: roots.count,
    derivativeZeroAtStart: roots.rootAtA,
    derivativeZeroAtEnd: roots.rootAtB,
    quadratic,
    certified:
      bernsteinSignConsistent && denominatorPositive && roots.count === 0,
  };
}

export function analyzeSplineExactly(segments: readonly SplineSegment[]): ExactSegmentReport[] {
  return segments.map((seg, i) => analyzeSegmentExactly(seg, i));
}
