import { DuplicateAbscissaError, InvalidInputError, NotMonotoneError } from './errors.ts';
import { pchipDerivatives } from './derivatives.ts';
import { analyzeQuadraticSign } from './quadratic.ts';
import type { QuadraticSignAnalysis } from './quadratic.ts';

/** Per-segment polynomial coefficients in the local coordinate theta in [0, 1]. */
export interface SegmentCoefficients {
  readonly index: number;
  readonly x0: number;
  readonly x1: number;
  readonly h: number;
  readonly y0: number;
  readonly y1: number;
  /** y1 - y0. */
  readonly secant: number;
  /** (y1 - y0) / h. */
  readonly secantSlope: number;
  /** Derivative parameters at the two endpoints. */
  readonly d0: number;
  readonly d1: number;
  /** Normalised slopes alpha = h d0 / secant, beta = h d1 / secant (0 for plateaus). */
  readonly alpha: number;
  readonly beta: number;
  /**
   * Shape parameter r = alpha + beta of the Delbourgo-Gregory rational cubic.
   * With this choice the cubic coefficient of the numerator vanishes and the
   * derivative numerator becomes exactly quadratic.
   */
  readonly r: number;
  /**
   * Numerator coefficients in ascending powers of theta (cubic form of the
   * Delbourgo-Gregory family).  The cubic coefficient is exactly 0 because
   * r = alpha + beta, so the numerator is genuinely quadratic.
   */
  readonly numerator: readonly [number, number, number, number];
  /** Denominator coefficients in ascending powers of theta. */
  readonly denominator: readonly [number, number, number];
  /**
   * W coefficients in ascending powers of theta, where
   * dP/dx = W(theta) / (h * D(theta)^2).  Always genuinely quadratic.
   */
  readonly derivativeNumerator: readonly [number, number, number];
}

export type SegmentTrend = 'increasing' | 'decreasing' | 'constant';

/** Closed-form proof that one segment keeps the trend of its data. */
export interface MonotonicityCertificate {
  readonly segmentIndex: number;
  readonly interval: readonly [number, number];
  readonly trend: SegmentTrend;
  readonly derivativeNumerator: readonly [number, number, number];
  /** Certified minimum of the denominator D on [0, 1]; must be > 0. */
  readonly denominatorMin: number;
  readonly analysis: QuadraticSignAnalysis;
  /** True when both the denominator is positive and sigma * W >= 0 on [0, 1]. */
  readonly holds: boolean;
  /** Human-readable closed-form argument. */
  readonly proof: string;
}

export interface VerificationReport {
  /** True when every segment certificate holds. */
  readonly holds: boolean;
  /** Smallest certified lower bound of sigma * W over all segments. */
  readonly certifiedMinMargin: number;
  /** Indices of segments whose certificate fails (empty when `holds`). */
  readonly failures: readonly number[];
  readonly certificates: readonly MonotonicityCertificate[];
}

export interface SplineOptions {
  /** Reject non-monotone ordinates with a NotMonotoneError. Default false. */
  readonly requireMonotone?: boolean;
  /**
   * Custom derivative parameters at the nodes (length n).  When omitted the
   * fixed Fritsch-Carlson/PCHIP rules are used.  Custom values are still
   * checked by the exact certificates, so `verify()` may report failure.
   */
  readonly derivatives?: readonly number[];
}

interface Segment extends SegmentCoefficients {
  readonly certificate: MonotonicityCertificate;
}

function buildSegment(
  index: number,
  x0: number,
  x1: number,
  y0: number,
  y1: number,
  d0: number,
  d1: number,
): Segment {
  const h = x1 - x0;
  const secant = y1 - y0;
  const secantSlope = secant / h;

  let r: number;
  let alpha: number;
  let beta: number;
  let numerator: [number, number, number, number];
  let denominator: [number, number, number];
  let w: [number, number, number];
  let trend: SegmentTrend;

  if (secant === 0) {
    // Plateau: the shape-preserving choice forces both endpoint derivatives
    // to zero (overriding any custom values), so the interpolant is exactly
    // constant and the derivative numerator is identically zero.
    d0 = 0;
    d1 = 0;
    r = 0;
    alpha = 0;
    beta = 0;
    numerator = [y0, -2 * y0, 2 * y0, 0];
    denominator = [1, -2, 2];
    w = [0, 0, 0];
    trend = 'constant';
  } else {
    alpha = (h * d0) / secant;
    beta = (h * d1) / secant;
    r = alpha + beta;
    const m = r * y0 + h * d0;
    // N(theta) = y0 (1-theta)^2 + m theta (1-theta) + y1 theta^2
    numerator = [y0, m - 2 * y0, y0 - m + y1, 0];
    denominator = [1, r - 2, 2 - r];
    w = [h * d0, 2 * (secant - h * d0), h * (d0 + d1) - 2 * secant];
    trend = secant > 0 ? 'increasing' : 'decreasing';
  }

  // D(theta) = (1-theta)^2 + r theta (1-theta) + theta^2 attains its minimum
  // on [0, 1] either at the endpoints (value 1) or at theta = 1/2
  // (value (2 + r) / 4).
  const denominatorMin = Math.min(1, (2 + r) / 4);

  const sigma: 1 | -1 = trend === 'decreasing' ? -1 : 1;
  const analysis = analyzeQuadraticSign(w[0], w[1], w[2], sigma);
  const holds = analysis.holds && denominatorMin > 0;

  const certificate: MonotonicityCertificate = {
    segmentIndex: index,
    interval: [x0, x1],
    trend,
    derivativeNumerator: w,
    denominatorMin,
    analysis,
    holds,
    proof: renderProof(trend, w, denominatorMin, analysis),
  };

  return {
    index,
    x0,
    x1,
    h,
    y0,
    y1,
    secant,
    secantSlope,
    d0,
    d1,
    alpha,
    beta,
    r,
    numerator,
    denominator,
    derivativeNumerator: w,
    certificate,
  };
}

function renderProof(
  trend: SegmentTrend,
  w: readonly [number, number, number],
  denominatorMin: number,
  analysis: QuadraticSignAnalysis,
): string {
  const wText = `W(theta) = ${w[0]} + (${w[1]}) theta + (${w[2]}) theta^2`;
  const dText = `min D(theta) = ${denominatorMin} > 0 on [0, 1]`;
  switch (analysis.classification) {
    case 'identically-zero':
      return `Plateau segment: secant = 0 forces d0 = d1 = 0, hence ${wText} is identically zero and P is exactly constant; ${dText}.`;
    case 'linear':
      return `${wText} is linear in theta, so its sign on [0, 1] is decided by the endpoints: min(sigma*W(0), sigma*W(1)) = ${analysis.certifiedMin}; ${dText}.`;
    case 'concave-endpoint-min':
      return `${wText} makes sigma*W concave (sigma*w2 < 0); a concave function attains its minimum on [0, 1] at an endpoint: min = ${analysis.certifiedMin}; ${dText}.`;
    case 'convex-vertex-outside':
      return `${wText} makes sigma*W convex with vertex theta* = ${analysis.vertexTheta} outside (0, 1); sigma*W is therefore monotone on [0, 1] with endpoint minimum ${analysis.certifiedMin}; ${dText}.`;
    case 'convex-vertex-inside':
      return `${wText} makes sigma*W convex with vertex theta* = ${analysis.vertexTheta} in (0, 1); the exact minimum is the vertex value (4 w0 w2 - w1^2)/(4 w2) = ${analysis.certifiedMin} (discriminant ${analysis.discriminant}); ${dText}.`;
  }
}

function isNondecreasing(ys: readonly number[]): boolean {
  for (let i = 1; i < ys.length; i++) if (ys[i] < ys[i - 1]) return false;
  return true;
}

function isNonincreasing(ys: readonly number[]): boolean {
  for (let i = 1; i < ys.length; i++) if (ys[i] > ys[i - 1]) return false;
  return true;
}

/**
 * Monotonicity-preserving rational piecewise-cubic interpolant.
 *
 * On every segment the interpolant is the Delbourgo-Gregory rational cubic
 * with shape parameter r = alpha + beta, for which the derivative numerator
 * is exactly quadratic; monotonicity of each segment is then certified by
 * exact quadratic root/vertex analysis (see MonotonicityCertificate).
 */
export class MonotoneRationalSpline {
  readonly nodes: readonly number[];
  readonly values: readonly number[];
  readonly derivatives: readonly number[];
  readonly segments: readonly SegmentCoefficients[];
  readonly certificates: readonly MonotonicityCertificate[];

  private readonly segmentList: readonly Segment[];

  constructor(xs: readonly number[], ys: readonly number[], options: SplineOptions = {}) {
    const points = normalizePoints(xs, ys);
    const n = points.xs.length;

    if (options.requireMonotone && n > 1 && !isNondecreasing(points.ys) && !isNonincreasing(points.ys)) {
      throw new NotMonotoneError();
    }

    let derivatives: readonly number[];
    if (options.derivatives !== undefined) {
      if (options.derivatives.length !== n) {
        throw new InvalidInputError(
          `Expected ${n} custom derivatives, got ${options.derivatives.length}`,
        );
      }
      for (const d of options.derivatives) {
        if (!Number.isFinite(d)) {
          throw new InvalidInputError('Custom derivatives must be finite numbers');
        }
      }
      derivatives = Array.from(options.derivatives);
    } else {
      derivatives = pchipDerivatives(points.xs, points.ys);
    }

    const segments: Segment[] = [];
    for (let i = 0; i + 1 < n; i++) {
      segments.push(
        buildSegment(
          i,
          points.xs[i],
          points.xs[i + 1],
          points.ys[i],
          points.ys[i + 1],
          derivatives[i],
          derivatives[i + 1],
        ),
      );
    }

    this.nodes = points.xs;
    this.values = points.ys;
    this.derivatives = Array.from(derivatives);
    this.segmentList = segments;
    this.segments = segments;
    this.certificates = segments.map((s) => s.certificate);
  }

  /** Number of distinct nodes after duplicate removal. */
  get size(): number {
    return this.nodes.length;
  }

  /** True when the ordinates are entirely nondecreasing or nonincreasing. */
  get isMonotoneData(): boolean {
    return isNondecreasing(this.values) || isNonincreasing(this.values);
  }

  /** Interpolated value at x (boundary segments extrapolate outside the knot range). */
  evaluate(x: number): number {
    if (!Number.isFinite(x)) {
      throw new InvalidInputError(`Cannot evaluate at non-finite x=${x}`);
    }
    if (this.segmentList.length === 0) {
      return this.values[0];
    }
    const seg = this.locate(x);
    const theta = (x - seg.x0) / seg.h;
    const [n0, n1, n2] = seg.numerator;
    const [a0, a1, a2] = seg.denominator;
    return (n0 + theta * (n1 + theta * n2)) / (a0 + theta * (a1 + theta * a2));
  }

  /** Exact derivative dP/dx at x via the closed form W(theta) / (h D(theta)^2). */
  derivative(x: number): number {
    if (!Number.isFinite(x)) {
      throw new InvalidInputError(`Cannot differentiate at non-finite x=${x}`);
    }
    if (this.segmentList.length === 0) {
      return 0;
    }
    const seg = this.locate(x);
    const theta = (x - seg.x0) / seg.h;
    const [w0, w1, w2] = seg.derivativeNumerator;
    const [a0, a1, a2] = seg.denominator;
    const denominator = a0 + theta * (a1 + theta * a2);
    const w = w0 + theta * (w1 + theta * w2);
    return w / (seg.h * denominator * denominator);
  }

  /** Exact, sampling-free monotonicity verification of every segment. */
  verify(): VerificationReport {
    const failures: number[] = [];
    let margin = Infinity;
    for (const certificate of this.certificates) {
      if (!certificate.holds) {
        failures.push(certificate.segmentIndex);
      }
      margin = Math.min(margin, certificate.analysis.certifiedMin);
    }
    if (this.certificates.length === 0) {
      margin = 0;
    }
    return {
      holds: failures.length === 0,
      certifiedMinMargin: margin,
      failures,
      certificates: this.certificates,
    };
  }

  /** Segment coefficients (the "coefficient output" of the library). */
  getCoefficients(): readonly SegmentCoefficients[] {
    return this.segments;
  }

  /** Per-segment non-negativity evidence for the derivative numerator. */
  getMonotonicityEvidence(): readonly MonotonicityCertificate[] {
    return this.certificates;
  }

  private locate(x: number): Segment {
    const segments = this.segmentList;
    const nodes = this.nodes;
    if (x <= nodes[0]) {
      return segments[0];
    }
    if (x >= nodes[nodes.length - 1]) {
      return segments[segments.length - 1];
    }
    let lo = 0;
    let hi = nodes.length - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (nodes[mid] <= x) {
        lo = mid;
      } else {
        hi = mid;
      }
    }
    return segments[lo];
  }
}

/** Validate inputs, remove exact duplicate points, reject conflicting duplicates. */
function normalizePoints(
  xs: readonly number[],
  ys: readonly number[],
): { xs: number[]; ys: number[] } {
  if (!Array.isArray(xs) || !Array.isArray(ys)) {
    throw new InvalidInputError('Nodes and ordinates must be arrays of numbers');
  }
  if (xs.length !== ys.length) {
    throw new InvalidInputError(`Length mismatch: ${xs.length} nodes vs ${ys.length} ordinates`);
  }
  if (xs.length === 0) {
    throw new InvalidInputError('At least one node is required');
  }
  const outX: number[] = [];
  const outY: number[] = [];
  for (let i = 0; i < xs.length; i++) {
    const x = xs[i];
    const y = ys[i];
    if (!Number.isFinite(x) || !Number.isFinite(y)) {
      throw new InvalidInputError(`Non-finite coordinate at index ${i}: (${x}, ${y})`);
    }
    if (outX.length > 0) {
      const lastX = outX[outX.length - 1];
      const lastY = outY[outY.length - 1];
      if (x < lastX) {
        throw new InvalidInputError(
          `Nodes must be sorted in nondecreasing order (x[${i}]=${x} < ${lastX})`,
        );
      }
      if (x === lastX) {
        if (y === lastY) {
          continue; // exact duplicate point: harmless, drop it
        }
        throw new DuplicateAbscissaError(x, lastY, y);
      }
    }
    outX.push(x);
    outY.push(y);
  }
  return { xs: outX, ys: outY };
}

/** Convenience factory. */
export function createSpline(
  xs: readonly number[],
  ys: readonly number[],
  options: SplineOptions = {},
): MonotoneRationalSpline {
  return new MonotoneRationalSpline(xs, ys, options);
}
