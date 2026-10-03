/** 统计与卡方拟合优度检验数学内核（零依赖） */

export function mean(xs: ArrayLike<number>): number {
  let sum = 0;
  for (let i = 0; i < xs.length; i++) sum += xs[i];
  return sum / xs.length;
}

/** 总体标准差 */
export function stdev(xs: ArrayLike<number>): number {
  const m = mean(xs);
  let acc = 0;
  for (let i = 0; i < xs.length; i++) {
    const d = xs[i] - m;
    acc += d * d;
  }
  return Math.sqrt(acc / xs.length);
}

const LANCZOS: readonly number[] = [
  0.99999999999980993, 676.5203681218851, -1259.1392167224028,
  771.32342877765313, -176.61502916214059, 12.507343278686905,
  -0.13857109526572012, 9.9843695780195716e-6, 1.5056327351493116e-7,
];

/** Lanczos 近似的 ln Γ(z) */
export function logGamma(z: number): number {
  if (z < 0.5) {
    return Math.log(Math.PI / Math.sin(Math.PI * z)) - logGamma(1 - z);
  }
  z -= 1;
  let x = LANCZOS[0];
  for (let i = 1; i < LANCZOS.length; i++) {
    x += LANCZOS[i] / (z + i);
  }
  const t = z + 7.5;
  return 0.5 * Math.log(2 * Math.PI) + (z + 0.5) * Math.log(t) - t + Math.log(x);
}

const GAMMA_EPS = 1e-14;
const GAMMA_ITER = 10000;
const FPMIN = 1e-300;

/** 级数展开计算正则化下不完全伽马 P(a,x)（x < a+1 时收敛快） */
function gammaPSeries(a: number, x: number): number {
  if (x === 0) return 0;
  const gln = logGamma(a);
  let ap = a;
  let sum = 1 / a;
  let del = sum;
  for (let n = 1; n <= GAMMA_ITER; n++) {
    ap += 1;
    del *= x / ap;
    sum += del;
    if (Math.abs(del) < Math.abs(sum) * GAMMA_EPS) break;
  }
  return sum * Math.exp(-x + a * Math.log(x) - gln);
}

/** 连分式计算正则化上不完全伽马 Q(a,x)（x >= a+1 时收敛快） */
function gammaQCF(a: number, x: number): number {
  const gln = logGamma(a);
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
  return Math.exp(-x + a * Math.log(x) - gln) * h;
}

/** 正则化上不完全伽马函数 Q(a,x) = Γ(a,x)/Γ(a) */
export function gammaQ(a: number, x: number): number {
  if (a <= 0) throw new RangeError('a must be > 0');
  if (x < 0) throw new RangeError('x must be >= 0');
  if (x < a + 1) return 1 - gammaPSeries(a, x);
  return gammaQCF(a, x);
}

export interface ChiSquareResult {
  chi2: number;
  df: number;
  expected: number;
}

/** 对“各桶计数应均匀”的假设计算卡方统计量 */
export function chiSquareUniform(counts: ArrayLike<number>): ChiSquareResult {
  const k = counts.length;
  let total = 0;
  for (let i = 0; i < k; i++) total += counts[i];
  const expected = total / k;
  let chi2 = 0;
  for (let i = 0; i < k; i++) {
    const d = counts[i] - expected;
    chi2 += (d * d) / expected;
  }
  return { chi2, df: k - 1, expected };
}

/** 卡方检验 p 值（生存函数）：p 越小越偏离均匀分布 */
export function chiSquarePValue(chi2: number, df: number): number {
  if (df <= 0) return 1;
  return gammaQ(df / 2, chi2 / 2);
}
