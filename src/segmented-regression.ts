import { Fraction } from "./fraction";

export type IntInput = bigint | number | string;

export interface Point {
  x: bigint;
  y: bigint;
}

/**
 * How to treat a segment whose x-values are all identical, which makes the
 * least-squares normal equations singular (denominator n*Sxx - Sx^2 == 0).
 *
 * - "constant": fit the exact least-squares minimizer for that degenerate
 *   design, i.e. the horizontal line y = mean(y). The segment is flagged
 *   with `singular: true` in the result.
 * - "reject": the segment is infeasible; the DP never selects it.
 * - "error": throw SingularSegmentError.
 *
 * There is no mode that silently divides by zero.
 */
export type SingularMode = "constant" | "reject" | "error";

export class SingularSegmentError extends Error {
  readonly start: number;
  readonly end: number;
  constructor(start: number, end: number) {
    super(
      `Segment [${start}, ${end}) is singular: all x-coordinates are equal, ` +
        `so the slope is not identifiable (n*Sxx - Sx^2 == 0).`
    );
    this.name = "SingularSegmentError";
    this.start = start;
    this.end = end;
  }
}

export interface SolverOptions {
  /** Maximum number of segments allowed. Must be >= 1. */
  maxSegments: number;
  /** Minimum points per segment. Defaults to 3. */
  minSegmentLength?: number;
  /** Handling of singular (constant-x) segments. Defaults to "constant". */
  singular?: SingularMode;
}

export interface SegmentResult {
  /** Inclusive start index into the input sequence. */
  start: number;
  /** Exclusive end index into the input sequence. */
  end: number;
  count: number;
  /** Exact least-squares slope. Exactly 0 for singular segments. */
  slope: Fraction;
  /** Exact least-squares intercept. */
  intercept: Fraction;
  /** Exact residual sum of squares for this segment. */
  sse: Fraction;
  /** True when every x in the segment is identical (degenerate fit). */
  singular: boolean;
}

export interface FitResult {
  segmentCount: number;
  /** Input indices at which a new segment begins (excluding index 0). */
  breakpoints: number[];
  segments: SegmentResult[];
  /** Exact total residual sum of squares. */
  totalSse: Fraction;
}

interface SegmentStats {
  slope: Fraction;
  intercept: Fraction;
  sse: Fraction;
  singular: boolean;
}

function toBigInt(value: IntInput, label: string): bigint {
  if (typeof value === "bigint") return value;
  if (typeof value === "number") {
    if (!Number.isInteger(value)) {
      throw new TypeError(`${label}: expected an integer, got ${value}`);
    }
    return BigInt(value);
  }
  const trimmed = value.trim();
  if (!/^[+-]?\d+$/.test(trimmed)) {
    throw new TypeError(`${label}: expected an integer string, got ${JSON.stringify(value)}`);
  }
  return BigInt(trimmed);
}

export function normalizePoints(xs: IntInput[], ys: IntInput[]): Point[] {
  if (xs.length !== ys.length) {
    throw new RangeError(
      `x and y must have the same length, got ${xs.length} and ${ys.length}`
    );
  }
  return xs.map((x, i) => ({
    x: toBigInt(x, `x[${i}]`),
    y: toBigInt(ys[i], `y[${i}]`),
  }));
}

/**
 * Exact least-squares line for one segment from prefix-sum aggregates.
 *
 * With Sx, Sy, Sxy, Sxx, Syy over n points and
 *   D = n*Sxx - Sx^2,  B = n*Sxy - Sx*Sy:
 *   slope     = B / D
 *   intercept = (Sy*D - B*Sx) / (n*D)
 *   sse       = (Syy*n*D - Sy^2*D - B^2) / (n*D)   (exact, always >= 0)
 *
 * D == 0 iff every x in the segment is identical (Cauchy-Schwarz equality),
 * which is detected explicitly before any division happens.
 */
function fitSegment(
  n: bigint,
  sx: bigint,
  sy: bigint,
  sxy: bigint,
  sxx: bigint,
  syy: bigint,
  start: number,
  end: number,
  mode: SingularMode
): SegmentStats | null {
  const denom = n * sxx - sx * sx;
  if (denom === 0n) {
    if (mode === "reject") return null;
    if (mode === "error") throw new SingularSegmentError(start, end);
    // "constant": the unique SSE-minimizing fit is the horizontal line
    // y = mean(y); its exact SSE is Syy - Sy^2/n.
    const intercept = Fraction.fromRatio(sy, n);
    const sse = Fraction.fromRatio(syy * n - sy * sy, n);
    return { slope: Fraction.of(0n), intercept, sse, singular: true };
  }
  const b = n * sxy - sx * sy;
  const slope = Fraction.fromRatio(b, denom);
  const intercept = Fraction.fromRatio(sy * denom - b * sx, n * denom);
  const sse = Fraction.fromRatio(syy * n * denom - sy * sy * denom - b * b, n * denom);
  return { slope, intercept, sse, singular: false };
}

/**
 * Fit at most `maxSegments` contiguous least-squares line segments to the
 * integer sequence (xs[i], ys[i]), minimizing the total squared error.
 *
 * All candidate comparisons use exact rational arithmetic (BigInt-backed),
 * so no rounding ever influences the chosen partition. Ties in total SSE
 * prefer fewer segments; remaining ties prefer the lexicographically
 * smallest breakpoint vector, making the result deterministic.
 */
export function fitSegmentedRegression(
  xs: IntInput[],
  ys: IntInput[],
  options: SolverOptions
): FitResult {
  const points = normalizePoints(xs, ys);
  const n = points.length;
  const minLen = options.minSegmentLength ?? 3;
  const mode: SingularMode = options.singular ?? "constant";

  if (!Number.isInteger(minLen) || minLen < 2) {
    throw new RangeError(`minSegmentLength must be an integer >= 2, got ${minLen}`);
  }
  if (!Number.isInteger(options.maxSegments) || options.maxSegments < 1) {
    throw new RangeError(`maxSegments must be an integer >= 1, got ${options.maxSegments}`);
  }
  if (n < minLen) {
    throw new RangeError(
      `Need at least ${minLen} points to fit one segment, got ${n}`
    );
  }
  const maxSegments = Math.min(options.maxSegments, Math.floor(n / minLen));

  // BigInt prefix sums: P[k] aggregates points[0..k).
  const px = new Array<bigint>(n + 1).fill(0n);
  const py = new Array<bigint>(n + 1).fill(0n);
  const pxy = new Array<bigint>(n + 1).fill(0n);
  const pxx = new Array<bigint>(n + 1).fill(0n);
  const pyy = new Array<bigint>(n + 1).fill(0n);
  for (let i = 0; i < n; i++) {
    const { x, y } = points[i];
    px[i + 1] = px[i] + x;
    py[i + 1] = py[i] + y;
    pxy[i + 1] = pxy[i] + x * y;
    pxx[i + 1] = pxx[i] + x * x;
    pyy[i + 1] = pyy[i] + y * y;
  }

  // Memoized exact segment stats; null means infeasible (rejected singular).
  const statsCache = new Map<number, SegmentStats | null>();
  function segmentStats(i: number, j: number): SegmentStats | null {
    const key = i * (n + 1) + j;
    let entry = statsCache.get(key);
    if (entry === undefined) {
      entry = fitSegment(
        BigInt(j - i),
        px[j] - px[i],
        py[j] - py[i],
        pxy[j] - pxy[i],
        pxx[j] - pxx[i],
        pyy[j] - pyy[i],
        i,
        j,
        mode
      );
      statsCache.set(key, entry);
    }
    return entry;
  }

  // rows[s][j] = best exact SSE covering points[0..j) with exactly s
  // segments; choices[s][j] = start index of the last segment. On exact SSE
  // ties within a row we keep the smallest start index, which yields the
  // lexicographically smallest breakpoint vector.
  const rows: (Fraction | null)[][] = [new Array(n + 1).fill(null)];
  const choices: (number | null)[][] = [new Array(n + 1).fill(null)];
  rows[0][0] = Fraction.of(0n);

  for (let s = 1; s <= maxSegments; s++) {
    const prev = rows[s - 1];
    const row: (Fraction | null)[] = new Array(n + 1).fill(null);
    const rowChoice: (number | null)[] = new Array(n + 1).fill(null);
    for (let j = s * minLen; j <= n; j++) {
      for (let i = (s - 1) * minLen; i <= j - minLen; i++) {
        const prevCost = prev[i];
        if (prevCost === null) continue;
        const stats = segmentStats(i, j);
        if (stats === null) continue;
        const candidate = prevCost.add(stats.sse);
        const current = row[j];
        if (current === null || candidate.cmp(current) < 0) {
          row[j] = candidate;
          rowChoice[j] = i;
        }
      }
    }
    rows.push(row);
    choices.push(rowChoice);
  }

  // Pick the segment count minimizing (totalSse, segmentCount): an exact
  // SSE tie always prefers fewer segments.
  let bestS = -1;
  let bestSse: Fraction | null = null;
  for (let s = 1; s <= maxSegments; s++) {
    const sse = rows[s][n];
    if (sse === null) continue;
    if (bestSse === null || sse.cmp(bestSse) < 0) {
      bestSse = sse;
      bestS = s;
    }
  }
  if (bestSse === null) {
    throw new RangeError(
      `No feasible partition of ${n} points into <= ${maxSegments} segments ` +
        `of length >= ${minLen} (singular mode: ${mode})`
    );
  }

  // Reconstruct the optimal partition from the choice pointers.
  const boundaries: number[] = [];
  let cursor = n;
  for (let s = bestS; s >= 1; s--) {
    const start = choices[s][cursor];
    if (start === null) {
      throw new Error(`Internal error: missing choice pointer at s=${s}, j=${cursor}`);
    }
    boundaries.push(start);
    cursor = start;
  }
  boundaries.reverse();

  const segments: SegmentResult[] = [];
  for (let k = 0; k < boundaries.length; k++) {
    const start = boundaries[k];
    const end = k + 1 < boundaries.length ? boundaries[k + 1] : n;
    const stats = segmentStats(start, end);
    if (stats === null) {
      throw new Error(`Internal error: selected infeasible segment [${start}, ${end})`);
    }
    segments.push({
      start,
      end,
      count: end - start,
      slope: stats.slope,
      intercept: stats.intercept,
      sse: stats.sse,
      singular: stats.singular,
    });
  }

  return {
    segmentCount: bestS,
    breakpoints: boundaries.slice(1),
    segments,
    totalSse: bestSse,
  };
}
