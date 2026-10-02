/**
 * 统计工具：误差函数 / 正态分布 CDF。
 * 仅使用可擦除（erasable）TypeScript 语法，便于 Node 22+ 直接运行。
 */

/** Abramowitz & Stegun 7.1.26 误差函数近似，|误差| < 1.5e-7 */
export function erf(x: number): number {
  const sign = x < 0 ? -1 : 1;
  const absX = Math.abs(x);
  const t = 1 / (1 + 0.3275911 * absX);
  const y =
    1 -
    (((((1.061405429 * t - 1.453152027) * t) + 1.421413741) * t -
      0.284496736) * t +
      0.254829592) *
      t *
      Math.exp(-absX * absX);
  return sign * y;
}

/** 标准正态分布 CDF */
export function normalCdf(x: number, mean: number, stdDev: number): number {
  return 0.5 * (1 + erf((x - mean) / (stdDev * Math.SQRT2)));
}
