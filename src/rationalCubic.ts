/**
 * Monotonicity-preserving rational piecewise cubic interpolation
 * (Delbourgo--Gregory family).
 *
 * Each segment uses the rational cubic in the local parameter
 * theta = (x - x0) / h:
 *
 *   P(theta) = N(theta) / D(theta),
 *   N(theta) = y1*t^3 + (r*y1 - h*d1)*t^2*(1-t) + (r*y0 + h*d0)*t*(1-t)^2 + y0*(1-t)^3,
 *   D(theta) = 1 + (r - 3)*t*(1-t),
 *
 * with endpoint derivatives d0, d1 from the Fritsch--Butland rule
 * (interior nodes) and the fixed Fritsch--Carlson one-sided three-point
 * rule (boundary nodes). Writing alpha = d0/delta and beta = d1/delta for
 * the secant slope delta, the shape parameter is fixed at
 *
 *   r = 1 + alpha + beta.
 *
 * Two exact consequences make the monotonicity proof elementary:
 *
 *  1. The cubic coefficient of N vanishes identically,
 *     (r-1)(y0-y1) + h(d0+d1) = 0, so N is the quadratic
 *     N(theta) = y0 + h*d0*theta + ((y1-y0) - h*d0 + (r-1)*(y1-y0))*theta^2
 *     and the derivative numerator is exactly the QUADRATIC
 *
 *       M(theta) = alpha*(1-theta)^2 + 2*theta*(1-theta) + beta*theta^2,
 *
 *     whose Bernstein coefficients [alpha, 1, beta] are all non-negative
 *     (alpha, beta >= 0 by the slope-limiting rules). Hence M >= 0 on the
 *     whole interval — with M(theta) > 0 for theta in (0, 1) — and the
 *     exact root analysis of dP/dx reduces to the discriminant of a
 *     quadratic, disc = 4*(1 - alpha*beta).
 *
 *  2. D(theta) = 1 + (alpha+beta-2)*theta*(1-theta) is bounded below by
 *     min(1, (alpha+beta+2)/4) >= 1/2 > 0 on [0, 1].
 *
 * Since dP/dx = delta * M(theta) / D(theta)^2, the derivative keeps the
 * sign of delta everywhere on the segment — analytically, not at sample
 * points. The Bernstein triplet and the denominator bound are exported
 * per segment as machine-checkable monotonicity evidence.
 */
import { analyzeSplineExactly, type ExactSegmentReport } from './exact.ts';


export class DuplicateAbscissaError extends Error {
  readonly x: number;
  readonly yFirst: number;
  readonly ySecond: number;
  constructor(x: number, yFirst: number, ySecond: number) {
    super(
      `duplicate abscissa x=${x} with conflicting ordinates ${yFirst} and ${ySecond}`,
    );
    this.name = 'DuplicateAbscissaError';
    this.x = x;
    this.yFirst = yFirst;
    this.ySecond = ySecond;
  }
}

export class NonMonotoneDataError extends Error {
  constructor() {
    super('input ordinates are not monotone and requireMonotone was set');
    this.name = 'NonMonotoneDataError';
  }
}

export interface MonotoneSplineOptions {
  /**
   * When true, reject data whose ordinates are neither non-decreasing nor
   * non-increasing. Default false: the interpolant is then monotone on
   * each segment in the direction of that segment's secant (no overshoot
   * anywhere, zero slope at local extrema).
   */
  requireMonotone?: boolean;
}

/** Coefficients of one segment, theta = (x - x0) / h in [0, 1]. */
export interface SegmentCoefficients {
  x0: number;
  x1: number;
  y0: number;
  y1: number;
  /** Endpoint derivatives dP/dx at x0 and x1. */
  d0: number;
  d1: number;
  h: number;
  /** Secant slope (y1 - y0) / h. */
  delta: number;
  /** Normalized endpoint slopes d0/delta, d1/delta (0 when delta = 0). */
  alpha: number;
  beta: number;
  /** Rational shape parameter r = 1 + alpha + beta. */
  r: number;
  /**
   * N(theta) = numerator[0] + numerator[1] t + numerator[2] t^2
   * (+ numerator[3] t^3, identically zero for this choice of r).
   */
  numerator: [number, number, number, number];
  /** D(theta) = denominator[0] + denominator[1] t + denominator[2] t^2. */
  denominator: [number, number, number];
}

/**
 * Analytic proof that the segment derivative keeps one sign on the whole
 * interval — valid everywhere, not only at sampled points.
 */
export interface MonotonicityEvidence {
  kind: 'constant' | 'rational';
  /** Sign of the secant slope: +1 increasing, -1 decreasing, 0 constant. */
  sign: -1 | 0 | 1;
  /**
   * Bernstein coefficients [c0, c1, c2] = [alpha, 1, beta] of the
   * quadratic numerator M(theta) of dQ/dtheta for the normalized segment
   * Q (Q(0)=0, Q(1)=1). All are >= 0 and c1 = 1 > 0, so M > 0 on the
   * open interval (0, 1) and M >= 0 on its closure.
   */
  bernstein: [number, number, number];
  /**
   * Positive lower bound of D(theta) on [0, 1]:
   * min(1, (alpha + beta + 2) / 4) >= 1/2.
   */
  denominatorMin: number;
  /** Discriminant 4*(1 - alpha*beta) of the quadratic M(theta). */
  derivativeDiscriminant: number;
  r: number;
  alpha: number;
  beta: number;
}

export interface SplineSegment {
  coeffs: SegmentCoefficients;
  evidence: MonotonicityEvidence;
}

export interface MonotoneRationalSpline {
  xs: number[];
  ys: number[];
  /** Derivatives dP/dx at the nodes (Hermite data actually used). */
  derivatives: number[];
  segments: SplineSegment[];
  /** True when the input ordinates are non-decreasing. */
  isNonDecreasing: boolean;
  /** True when the input ordinates are non-increasing. */
  isNonIncreasing: boolean;
  evaluate(x: number): number;
  derivative(x: number): number;
  /**
   * Re-verify every segment with exact rational arithmetic (Bernstein
   * certificate + quadratic discriminant + Sturm root count).
   */
  verifyMonotonicity(): ExactSegmentReport[];
}

function signum(v: number): -1 | 0 | 1 {
  return v > 0 ? 1 : v < 0 ? -1 : 0;
}

/** Fixed endpoint rule: one-sided three-point estimate, then Fritsch--Carlson limiting. */
function endpointDerivative(h0: number, h1: number, delta0: number, delta1: number): number {
  const d = ((2 * h0 + h1) * delta0 - h0 * delta1) / (h0 + h1);
  if (signum(d) !== signum(delta0)) return 0;
  if (signum(delta0) !== signum(delta1) && Math.abs(d) > 3 * Math.abs(delta0)) {
    return 3 * delta0;
  }
  return d;
}

/** Fritsch--Butland interior rule: weighted harmonic mean of secant slopes. */
function interiorDerivative(
  hPrev: number,
  hNext: number,
  deltaPrev: number,
  deltaNext: number,
): number {
  if (deltaPrev === 0 || deltaNext === 0 || signum(deltaPrev) !== signum(deltaNext)) {
    return 0;
  }
  const w1 = 2 * hNext + hPrev;
  const w2 = hNext + 2 * hPrev;
  return (w1 + w2) / (w1 / deltaPrev + w2 / deltaNext);
}

function validateAndDedupe(
  xs: readonly number[],
  ys: readonly number[],
): { x: number[]; y: number[] } {
  if (xs.length !== ys.length) {
    throw new Error(`xs (${xs.length}) and ys (${ys.length}) must have equal length`);
  }
  if (xs.length < 2) {
    throw new Error('at least two nodes are required');
  }
  const x: number[] = [];
  const y: number[] = [];
  for (let i = 0; i < xs.length; i++) {
    if (!Number.isFinite(xs[i]) || !Number.isFinite(ys[i])) {
      throw new Error(`non-finite input at index ${i}`);
    }
    if (x.length > 0) {
      const lastX = x[x.length - 1];
      const lastY = y[y.length - 1];
      if (xs[i] === lastX) {
        // Repeated abscissa: an identical ordinate is merged, a
        // conflicting ordinate is rejected.
        if (ys[i] === lastY) continue;
        throw new DuplicateAbscissaError(xs[i], lastY, ys[i]);
      }
      if (xs[i] < lastX) {
        throw new Error(`abscissae must be non-decreasing (index ${i})`);
      }
    }
    x.push(xs[i]);
    y.push(ys[i]);
  }
  if (x.length < 2) {
    throw new Error('at least two distinct abscissae are required');
  }
  return { x, y };
}

function buildSegment(
  x0: number,
  x1: number,
  y0: number,
  y1: number,
  d0: number,
  d1: number,
): SplineSegment {
  const h = x1 - x0;
  const delta = (y1 - y0) / h;

  if (delta === 0) {
    // Flat data force d0 = d1 = 0 (Fritsch--Butland / endpoint limiting),
    // so the segment degenerates to the constant y0: N = y0 * D.
    const r = 1;
    const numerator: [number, number, number, number] = [
      y0,
      (r - 3) * y0,
      (3 - r) * y0,
      0,
    ];
    const denominator: [number, number, number] = [1, r - 3, -(r - 3)];
    return {
      coeffs: { x0, x1, y0, y1, d0, d1, h, delta, alpha: 0, beta: 0, r, numerator, denominator },
      evidence: {
        kind: 'constant',
        sign: 0,
        bernstein: [0, 0, 0],
        denominatorMin: 1 / 2,
        derivativeDiscriminant: 0,
        r,
        alpha: 0,
        beta: 0,
      },
    };
  }

  const alpha = d0 / delta;
  const beta = d1 / delta;
  const r = 1 + alpha + beta;
  // The theta^3 coefficient (r-1)(y0-y1) + h(d0+d1) is identically zero
  // for r = 1 + alpha + beta; it is emitted as the exact value 0.
  const numerator: [number, number, number, number] = [
    y0,
    (r - 3) * y0 + h * d0,
    (3 - 2 * r) * y0 - 2 * h * d0 + r * y1 - h * d1,
    0,
  ];
  const denominator: [number, number, number] = [1, r - 3, -(r - 3)];
  return {
    coeffs: { x0, x1, y0, y1, d0, d1, h, delta, alpha, beta, r, numerator, denominator },
    evidence: {
      kind: 'rational',
      sign: signum(delta),
      bernstein: [alpha, 1, beta],
      denominatorMin: alpha + beta >= 2 ? 1 : (alpha + beta + 2) / 4,
      derivativeDiscriminant: 4 * (1 - alpha * beta),
      r,
      alpha,
      beta,
    },
  };
}

export function createMonotoneRationalSpline(
  xs: readonly number[],
  ys: readonly number[],
  options: MonotoneSplineOptions = {},
): MonotoneRationalSpline {
  const { x, y } = validateAndDedupe(xs, ys);
  const n = x.length;

  const h: number[] = [];
  const delta: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    h.push(x[i + 1] - x[i]);
    delta.push((y[i + 1] - y[i]) / h[i]);
  }

  const isNonDecreasing = delta.every((d) => d >= 0);
  const isNonIncreasing = delta.every((d) => d <= 0);
  if (options.requireMonotone && !isNonIncreasing && !isNonDecreasing) {
    throw new NonMonotoneDataError();
  }

  const derivatives: number[] = new Array<number>(n);
  if (n === 2) {
    derivatives[0] = delta[0];
    derivatives[1] = delta[0];
  } else {
    derivatives[0] = endpointDerivative(h[0], h[1], delta[0], delta[1]);
    derivatives[n - 1] = endpointDerivative(
      h[n - 2],
      h[n - 3],
      delta[n - 2],
      delta[n - 3],
    );
    for (let i = 1; i < n - 1; i++) {
      derivatives[i] = interiorDerivative(h[i - 1], h[i], delta[i - 1], delta[i]);
    }
  }

  const segments: SplineSegment[] = [];
  for (let i = 0; i < n - 1; i++) {
    segments.push(buildSegment(x[i], x[i + 1], y[i], y[i + 1], derivatives[i], derivatives[i + 1]));
  }

  function locate(xq: number): number {
    if (xq < x[0] || xq > x[n - 1]) {
      throw new RangeError(
        `evaluation point ${xq} outside the interpolation range [${x[0]}, ${x[n - 1]}]`,
      );
    }
    let lo = 0;
    let hi = n - 1;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (x[mid] <= xq) lo = mid;
      else hi = mid;
    }
    return lo;
  }

  function evaluate(xq: number): number {
    const i = locate(xq);
    const c = segments[i].coeffs;
    const t = (xq - c.x0) / c.h;
    const u = 1 - t;
    // Bernstein-form evaluation: stable and exact at the endpoints.
    const num =
      c.y1 * t * t * t +
      (c.r * c.y1 - c.h * c.d1) * t * t * u +
      (c.r * c.y0 + c.h * c.d0) * t * u * u +
      c.y0 * u * u * u;
    const den = 1 + (c.r - 3) * t * u;
    return num / den;
  }

  function derivative(xq: number): number {
    const i = locate(xq);
    const seg = segments[i];
    const c = seg.coeffs;
    if (seg.evidence.kind === 'constant') return 0;
    const t = (xq - c.x0) / c.h;
    const u = 1 - t;
    const b = seg.evidence.bernstein;
    // dP/dx = delta * M(t) / D(t)^2 with the quadratic
    // M(t) = b0 (1-t)^2 + 2 b1 t (1-t) + b2 t^2.
    const m = b[0] * u * u + 2 * b[1] * t * u + b[2] * t * t;
    const den = 1 + (c.r - 3) * t * u;
    return (c.delta * m) / (den * den);
  }

  return {
    xs: x,
    ys: y,
    derivatives,
    segments,
    isNonDecreasing,
    isNonIncreasing,
    evaluate,
    derivative,
    verifyMonotonicity: () => analyzeSplineExactly(segments),
  };
}
