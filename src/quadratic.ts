/**
 * Exact sign analysis of a quadratic on the closed unit interval.
 *
 * The derivative of every spline segment has the closed form
 *
 *     dP/dx = W(theta) / (h * D(theta)^2),   theta in [0, 1],
 *
 * where D > 0 and W is a *quadratic*.  Monotonicity of a segment is
 * therefore decided exactly by the sign analysis of one quadratic —
 * no sampling is involved anywhere in the certificate.
 */

export type QuadraticClassification =
  /** W is identically zero (plateau segment). */
  | 'identically-zero'
  /** W is linear; its minimum on [0, 1] is attained at an endpoint. */
  | 'linear'
  /** sigma*W is concave; its minimum on [0, 1] is attained at an endpoint. */
  | 'concave-endpoint-min'
  /** sigma*W is convex with vertex outside (0, 1); endpoint minimum. */
  | 'convex-vertex-outside'
  /** sigma*W is convex with vertex inside (0, 1); minimum is the vertex value. */
  | 'convex-vertex-inside';

export interface QuadraticSignAnalysis {
  readonly classification: QuadraticClassification;
  /** Vertex abscissa -w1 / (2 w2), or null when W is not a genuine quadratic. */
  readonly vertexTheta: number | null;
  /**
   * Certified lower bound for sigma * W(theta) over theta in [0, 1],
   * computed in closed form.  `>= 0` proves the segment keeps its trend.
   */
  readonly certifiedMin: number;
  /** Discriminant w1^2 - 4 w0 w2 (null for degenerate/linear W). */
  readonly discriminant: number | null;
  /** Exact real roots of W that lie in the closed interval [0, 1]. */
  readonly rootsInUnitInterval: readonly number[];
  /** Whether the certificate proves sigma * W >= 0 on [0, 1]. */
  readonly holds: boolean;
  /** Absolute tolerance used for the `holds` verdict (exact value is `certifiedMin`). */
  readonly tolerance: number;
}

/**
 * Analyse q(theta) = sigma * (w0 + w1 theta + w2 theta^2) on theta in [0, 1]
 * and produce a closed-form non-negativity certificate.
 *
 * @param sigma +1 to certify W >= 0 (increasing segment), -1 for W <= 0.
 */
export function analyzeQuadraticSign(
  w0: number,
  w1: number,
  w2: number,
  sigma: 1 | -1,
): QuadraticSignAnalysis {
  const scale = Math.max(1, Math.abs(w0), Math.abs(w1), Math.abs(w2));
  const tolerance = 1e-12 * scale;

  const q0 = sigma * w0;
  const q1 = sigma * (w0 + w1 + w2);

  // Exact roots of W via the quadratic formula (numerically stable form).
  let discriminant: number | null = null;
  const roots: number[] = [];
  if (w2 !== 0) {
    discriminant = w1 * w1 - 4 * w0 * w2;
    if (discriminant > 0) {
      const sqrtD = Math.sqrt(discriminant);
      const q = -0.5 * (w1 + Math.sign(w1 === 0 ? 1 : w1) * sqrtD);
      if (q !== 0) {
        roots.push(q / w2, w0 / q);
      } else {
        roots.push((-w1 - sqrtD) / (2 * w2), (-w1 + sqrtD) / (2 * w2));
      }
    } else if (discriminant === 0) {
      roots.push(-w1 / (2 * w2));
    }
  } else if (w1 !== 0) {
    roots.push(-w0 / w1);
  }
  const rootsInUnitInterval = roots
    .filter((t) => t >= 0 && t <= 1)
    .map((t) => (t === 0 ? 0 : t)) // normalise -0
    .sort((a, b) => a - b);

  let classification: QuadraticClassification;
  let vertexTheta: number | null = null;
  let certifiedMin: number;

  if (w0 === 0 && w1 === 0 && w2 === 0) {
    classification = 'identically-zero';
    certifiedMin = 0;
  } else if (w2 === 0) {
    classification = 'linear';
    certifiedMin = Math.min(q0, q1);
  } else {
    vertexTheta = -w1 / (2 * w2);
    if (sigma * w2 < 0) {
      // Concave: minimum over a closed interval is attained at an endpoint.
      classification = 'concave-endpoint-min';
      certifiedMin = Math.min(q0, q1);
    } else if (vertexTheta <= 0 || vertexTheta >= 1) {
      // Convex with vertex outside (0, 1): monotone on [0, 1], endpoint min.
      classification = 'convex-vertex-outside';
      certifiedMin = Math.min(q0, q1);
    } else {
      // Convex with interior vertex: exact minimum is the vertex value
      // q(theta*) = (4 w0 w2 - w1^2) / (4 w2).
      classification = 'convex-vertex-inside';
      certifiedMin = sigma * (w0 - (w1 * w1) / (4 * w2));
    }
  }

  return {
    classification,
    vertexTheta,
    certifiedMin: certifiedMin + 0, // normalise -0
    discriminant,
    rootsInUnitInterval,
    holds: certifiedMin >= -tolerance,
    tolerance,
  };
}
