/**
 * Core statistics: error function, normal tail probabilities, mid-ranks and
 * the tie-corrected two-sample Mann-Whitney U test.
 */

/** Abramowitz & Stegun style approximation, |error| <= 1.5e-7. */
export function erf(x: number): number {
  const sign = x < 0 ? -1 : 1;
  const ax = Math.abs(x);
  const t = 1 / (1 + 0.3275911 * ax);
  const y =
    1 -
    (((((1.061405429 * t - 1.453152027) * t) + 1.421413741) * t -
      0.284496736) * t +
      0.254829592) *
      t *
      Math.exp(-ax * ax);
  return sign * y;
}

/** Standard normal CDF Phi(z). */
export function normalCdf(z: number): number {
  return 0.5 * (1 + erf(z / Math.SQRT2));
}

/**
 * Assign 1-based mid-ranks (average ranks for ties) to `values`,
 * returned in the original order.
 */
export function midRanks(values: number[]): number[] {
  const indexed = values.map((v, i) => ({ v, i }));
  indexed.sort((a, b) => a.v - b.v);
  const ranks = new Array<number>(values.length);
  let i = 0;
  while (i < indexed.length) {
    let j = i;
    while (j + 1 < indexed.length && indexed[j + 1].v === indexed[i].v) j++;
    const avgRank = (i + 1 + (j + 1)) / 2;
    for (let k = i; k <= j; k++) ranks[indexed[k].i] = avgRank;
    i = j + 1;
  }
  return ranks;
}

export interface MannWhitneyResult {
  n1: number; // baseline sample size
  n2: number; // canary sample size
  uCanary: number;
  uBaseline: number;
  /** Tie-corrected standard deviation of U. 0 when every value is identical. */
  sigmaU: number;
  /** z = (uCanary - n1*n2/2) / sigmaU; positive means canary ranks higher. */
  z: number;
  /** One-sided p for "canary > baseline" (upper tail). */
  pUpper: number;
  /** One-sided p for "canary < baseline" (lower tail). */
  pLower: number;
  /** Two-sided p value. */
  pTwoSided: number;
}

/**
 * Two-sample Mann-Whitney U test with exact tie-corrected variance:
 *
 *   sigma_U = sqrt( n1*n2/12 * ( (N+1) - sum(t_k^3 - t_k) / (N*(N-1)) ) )
 *
 * When all values are identical sigma_U is 0; the result is then a zero
 * difference (z = 0, pTwoSided = 1) and never a NaN.
 */
export function mannWhitneyU(
  baseline: number[],
  canary: number[],
): MannWhitneyResult {
  const n1 = baseline.length;
  const n2 = canary.length;
  if (n1 === 0 || n2 === 0) {
    throw new Error(
      "mannWhitneyU requires non-empty baseline and canary samples",
    );
  }
  const N = n1 + n2;

  const combined = baseline.concat(canary);
  const ranks = midRanks(combined);
  let rankSumCanary = 0;
  for (let i = n1; i < N; i++) rankSumCanary += ranks[i];

  const uCanary = rankSumCanary - (n2 * (n2 + 1)) / 2;
  const uBaseline = n1 * n2 - uCanary;

  // Tie correction: sum of (t^3 - t) over groups of equal values.
  const sorted = combined.slice().sort((a, b) => a - b);
  let tieSum = 0;
  let runLength = 1;
  for (let i = 1; i <= N; i++) {
    if (i < N && sorted[i] === sorted[i - 1]) {
      runLength++;
    } else {
      if (runLength > 1) tieSum += runLength ** 3 - runLength;
      runLength = 1;
    }
  }

  const variance = ((n1 * n2) / 12) * (N + 1 - tieSum / (N * (N - 1)));
  const sigmaU = Math.sqrt(Math.max(variance, 0));

  if (sigmaU === 0) {
    // Degenerate case: every observation identical -> zero difference.
    return {
      n1,
      n2,
      uCanary,
      uBaseline,
      sigmaU: 0,
      z: 0,
      pUpper: 0.5,
      pLower: 0.5,
      pTwoSided: 1,
    };
  }

  const z = (uCanary - (n1 * n2) / 2) / sigmaU;
  const cdf = normalCdf(z);
  const pUpper = 1 - cdf;
  const pLower = cdf;
  const pTwoSided = Math.min(1, 2 * Math.min(pUpper, pLower));

  return { n1, n2, uCanary, uBaseline, sigmaU, z, pUpper, pLower, pTwoSided };
}

/** Quantile with linear interpolation (R type 7), q in [0, 1]. */
export function quantile(values: number[], q: number): number {
  if (values.length === 0) throw new Error("quantile of empty sample");
  const sorted = values.slice().sort((a, b) => a - b);
  const pos = (sorted.length - 1) * q;
  const lo = Math.floor(pos);
  const hi = Math.ceil(pos);
  if (lo === hi) return sorted[lo];
  const frac = pos - lo;
  return sorted[lo] * (1 - frac) + sorted[hi] * frac;
}
