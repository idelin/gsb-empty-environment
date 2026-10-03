/** Descriptive statistics + chi-square goodness-of-fit machinery. */

export function mean(xs: ArrayLike<number>): number {
  let sum = 0;
  for (let i = 0; i < xs.length; i++) sum += xs[i];
  return sum / xs.length;
}

export function stddev(xs: ArrayLike<number>, mu = mean(xs)): number {
  let acc = 0;
  for (let i = 0; i < xs.length; i++) {
    const d = xs[i] - mu;
    acc += d * d;
  }
  return Math.sqrt(acc / xs.length);
}

export function minMax(xs: ArrayLike<number>): { min: number; max: number } {
  let min = Infinity;
  let max = -Infinity;
  for (let i = 0; i < xs.length; i++) {
    const v = xs[i];
    if (v < min) min = v;
    if (v > max) max = v;
  }
  return { min, max };
}

/** Pearson's chi-square statistic against a uniform expectation. */
export function chiSquareUniform(observed: ArrayLike<number>): {
  stat: number;
  df: number;
  expected: number;
} {
  const buckets = observed.length;
  let total = 0;
  for (let i = 0; i < buckets; i++) total += observed[i];
  const expected = total / buckets;
  let stat = 0;
  for (let i = 0; i < buckets; i++) {
    const d = observed[i] - expected;
    stat += (d * d) / expected;
  }
  return { stat, df: buckets - 1, expected };
}

const LANCZOS = [
  0.99999999999980993, 676.5203681218851, -1259.1392167224028,
  771.32342877765313, -176.61502916214059, 12.507343278686905,
  -0.13857109526572012, 9.9843695780195716e-6, 1.5056327351493116e-7,
];

/** Lanczos approximation of ln(Gamma(z)). */
export function logGamma(z: number): number {
  if (z < 0.5) {
    return Math.log(Math.PI / Math.sin(Math.PI * z)) - logGamma(1 - z);
  }
  z -= 1;
  let x = LANCZOS[0];
  for (let i = 1; i < LANCZOS.length; i++) x += LANCZOS[i] / (z + i);
  const t = z + 7.5;
  return 0.5 * Math.log(2 * Math.PI) + (z + 0.5) * Math.log(t) - t + Math.log(x);
}

const GAMMA_EPS = 1e-14;
const GAMMA_ITER = 10_000;
const FPMIN = 1e-300;

/**
 * Regularized upper incomplete gamma Q(a, x) = Gamma(a, x) / Gamma(a).
 * Numerical Recipes gser/gcf pair.
 */
export function gammaQ(a: number, x: number): number {
  if (x < 0 || a <= 0) throw new RangeError("bad args for gammaQ");
  if (x === 0) return 1;
  if (x < a + 1) {
    // Series representation for P, then Q = 1 - P.
    let ap = a;
    let sum = 1 / a;
    let del = sum;
    for (let n = 1; n <= GAMMA_ITER; n++) {
      ap += 1;
      del *= x / ap;
      sum += del;
      if (Math.abs(del) < Math.abs(sum) * GAMMA_EPS) break;
    }
    const p = sum * Math.exp(-x + a * Math.log(x) - logGamma(a));
    return Math.min(1, Math.max(0, 1 - p));
  }
  // Continued fraction for Q directly.
  let b = x + 1 - a;
  let c = 1 / FPMIN;
  let d = 1 / b;
  let h = d;
  for (let i = 1; i <= GAMMA_ITER; i++) {
    const an = -i * (i - a);
    b += 2;
    d = an * d + b;
    if (Math.abs(d) < FPMIN) d = FPMIN;
    c = b + an / c;
    if (Math.abs(c) < FPMIN) c = FPMIN;
    d = 1 / d;
    const del = d * c;
    h *= del;
    if (Math.abs(del - 1) < GAMMA_EPS) break;
  }
  const q = Math.exp(-x + a * Math.log(x) - logGamma(a)) * h;
  return Math.min(1, Math.max(0, q));
}

/** Survival function (p-value) of the chi-square distribution. */
export function chiSquarePValue(stat: number, df: number): number {
  return gammaQ(df / 2, stat / 2);
}
