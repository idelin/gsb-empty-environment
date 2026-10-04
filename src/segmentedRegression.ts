import { Fraction } from "./fraction.ts";

export interface Segment {
  /** Inclusive start index into the input arrays. */
  start: number;
  /** Exclusive end index into the input arrays. */
  end: number;
  /** Exact least-squares slope. Zero for degenerate segments. */
  slope: Fraction;
  /** Exact least-squares intercept. Equals mean(y) for degenerate segments. */
  intercept: Fraction;
  /** Exact residual sum of squares of this segment. */
  sse: Fraction;
  /**
   * True when every x in the segment is identical (Sxx = 0), so the design
   * matrix is rank-deficient and the slope is not identifiable. The fit
   * falls back to the horizontal line y = mean(y), which is the minimum-norm
   * least-squares solution. This case is detected explicitly — no division
   * by zero ever happens silently.
   */
  degenerate: boolean;
}

export interface FitResult {
  segments: Segment[];
  /** Indices (into the input arrays) where a new segment starts, excluding 0. */
  breakpoints: number[];
  /** Exact total residual sum of squares. */
  totalSSE: Fraction;
}

interface SegmentCost {
  sse: Fraction;
  slope: Fraction;
  intercept: Fraction;
  degenerate: boolean;
}

/**
 * Fits a piecewise-linear function with at most `maxSegments` segments to the
 * integer sequence (xs[i], ys[i]), minimizing the total residual sum of
 * squares. Every segment must contain at least `minSegmentLength` points
 * (default 3). Among solutions with equal total SSE, the one with the fewest
 * segments is chosen; remaining ties are broken deterministically by
 * preferring earlier breakpoints.
 *
 * All candidate errors are compared as exact rationals (BigInt fractions),
 * so ties and near-ties are decided exactly, never by floating-point
 * rounding.
 */
export function fitSegmentedRegression(
  xs: readonly (number | bigint)[],
  ys: readonly (number | bigint)[],
  maxSegments: number,
  minSegmentLength: number = 3,
): FitResult {
  if (xs.length !== ys.length) {
    throw new Error(`x and y must have the same length (got ${xs.length} and ${ys.length})`);
  }
  const n = xs.length;
  if (!Number.isInteger(maxSegments) || maxSegments < 1) {
    throw new Error(`maxSegments must be a positive integer (got ${maxSegments})`);
  }
  if (!Number.isInteger(minSegmentLength) || minSegmentLength < 2) {
    throw new Error(`minSegmentLength must be an integer >= 2 (got ${minSegmentLength})`);
  }
  if (n < minSegmentLength) {
    throw new Error(`need at least ${minSegmentLength} points, got ${n}`);
  }
  if (maxSegments * minSegmentLength > n) {
    throw new Error(
      `infeasible: ${maxSegments} segments of at least ${minSegmentLength} points ` +
        `need at least ${maxSegments * minSegmentLength} points, got ${n}`,
    );
  }

  const x = xs.map(toBigIntExact);
  const y = ys.map(toBigIntExact);

  // BigInt prefix sums so every segment statistic is an exact integer.
  const px = newArray(n + 1, 0n);
  const py = newArray(n + 1, 0n);
  const pxx = newArray(n + 1, 0n);
  const pxy = newArray(n + 1, 0n);
  const pyy = newArray(n + 1, 0n);
  for (let i = 0; i < n; i++) {
    px[i + 1] = px[i] + x[i];
    py[i + 1] = py[i] + y[i];
    pxx[i + 1] = pxx[i] + x[i] * x[i];
    pxy[i + 1] = pxy[i] + x[i] * y[i];
    pyy[i + 1] = pyy[i] + y[i] * y[i];
  }

  // Exact least-squares cost of one segment covering indices [j, i).
  const segmentCost = (j: number, i: number): SegmentCost => {
    const count = BigInt(i - j);
    const sx = px[i] - px[j];
    const sy = py[i] - py[j];
    const sxx = pxx[i] - pxx[j];
    const sxy = pxy[i] - pxy[j];
    const syy = pyy[i] - pyy[j];

    // Centered (exact integer) second moments:
    //   Sxx = n*sum(x^2) - sum(x)^2  (= n^2 * Var(x), >= 0)
    //   Sxy = n*sum(xy)  - sum(x)*sum(y)
    //   Syy = n*sum(y^2) - sum(y)^2
    const Sxx = count * sxx - sx * sx;
    const Sxy = count * sxy - sx * sy;
    const Syy = count * syy - sy * sy;

    if (Sxx === 0n) {
      // All x in [j, i) are identical: the normal equations are singular and
      // the slope is not identifiable. Handle explicitly (never divide by
      // Sxx here): the minimum-norm least-squares fit is the horizontal line
      // y = mean(y), with SSE = sum((y - mean(y))^2) = Syy / n.
      return {
        sse: new Fraction(Syy, count),
        slope: Fraction.zero(),
        intercept: new Fraction(sy, count),
        degenerate: true,
      };
    }

    // slope = Sxy / Sxx
    // intercept = (sum(y) - slope * sum(x)) / n = (sy*Sxx - Sxy*sx) / (n*Sxx)
    // SSE = Syy/n - Sxy^2/(n*Sxx) = (Syy*Sxx - Sxy^2) / (n*Sxx)  (>= 0)
    const slope = new Fraction(Sxy, Sxx);
    const intercept = new Fraction(sy * Sxx - Sxy * sx, count * Sxx);
    const sse = new Fraction(Syy * Sxx - Sxy * Sxy, count * Sxx);
    return { sse, slope, intercept, degenerate: false };
  };

  // dp[k][i] = minimum total SSE covering the first i points with exactly k
  // segments; parent[k][i] = start index of the last segment.
  const dp: (Fraction | null)[][] = [];
  const parent: number[][] = [];
  for (let k = 0; k <= maxSegments; k++) {
    dp.push(newArray(n + 1, null));
    parent.push(newArray(n + 1, -1));
  }
  dp[0][0] = Fraction.zero();

  for (let k = 1; k <= maxSegments; k++) {
    // i must leave room for k-1 earlier segments and close with >= m points.
    for (let i = k * minSegmentLength; i <= n; i++) {
      let best: Fraction | null = null;
      let bestJ = -1;
      // j ascending: on exact ties the earliest breakpoint wins (strict <).
      for (let j = (k - 1) * minSegmentLength; j <= i - minSegmentLength; j++) {
        const prev = dp[k - 1][j];
        if (prev === null) continue;
        const candidate = prev.add(segmentCost(j, i).sse);
        if (best === null || candidate.cmp(best) < 0) {
          best = candidate;
          bestJ = j;
        }
      }
      dp[k][i] = best;
      parent[k][i] = bestJ;
    }
  }

  // Fewest segments among those attaining the exact minimum total SSE.
  let bestK = -1;
  let bestTotal: Fraction | null = null;
  for (let k = 1; k <= maxSegments; k++) {
    const total = dp[k][n];
    if (total === null) continue;
    if (bestTotal === null || total.cmp(bestTotal) < 0) {
      bestTotal = total;
      bestK = k;
    }
  }
  if (bestK < 0 || bestTotal === null) {
    throw new Error("no feasible segmentation");
  }

  const segments: Segment[] = [];
  let end = n;
  for (let k = bestK; k >= 1; k--) {
    const start = parent[k][end];
    const cost = segmentCost(start, end);
    segments.push({
      start,
      end,
      slope: cost.slope,
      intercept: cost.intercept,
      sse: cost.sse,
      degenerate: cost.degenerate,
    });
    end = start;
  }
  segments.reverse();

  return {
    segments,
    breakpoints: segments.slice(1).map((s) => s.start),
    totalSSE: bestTotal,
  };
}

function toBigIntExact(value: number | bigint): bigint {
  if (typeof value === "bigint") return value;
  if (!Number.isInteger(value)) {
    throw new Error(`coordinates must be integers, got ${value}`);
  }
  return BigInt(value);
}

function newArray<T>(length: number, fill: T): T[] {
  const arr = new Array<T>(length);
  arr.fill(fill);
  return arr;
}
