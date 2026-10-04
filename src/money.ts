import type { RationalRate } from './rate.ts';

/**
 * 金额一律以“分”为单位的整数表示（number，安全范围内），
 * 涉及乘法放大的中间计算使用 BigInt，避免任何浮点近似。
 */

/** 按分舍入（四舍五入，half-up）：计算 round(cents * rate)。 */
export function interestOf(balanceCents: number, rate: RationalRate): number {
  const numerator = BigInt(balanceCents) * rate.num;
  // half-up: round(a/b) = floor((2a + b) / 2b)，a、b 均非负
  const rounded = (2n * numerator + rate.den) / (2n * rate.den);
  return Number(rounded);
}

/** 四舍五入（half-up）计算 BigInt 分数 x/d，x、d 非负。 */
function roundDiv(x: bigint, d: bigint): bigint {
  return (2n * x + d) / (2n * d);
}

/**
 * 等额本息月供（按分舍入）：
 *   PMT = P * r * (1+r)^n / ((1+r)^n - 1)，r = num/den
 * 推导为纯整数形式：
 *   PMT = P * num * (den+num)^n / (den * ((den+num)^n - den^n))
 * r = 0 时退化为 P / n。
 */
export function annuityPayment(principalCents: number, periods: number, rate: RationalRate): number {
  if (periods <= 0) throw new Error('期数必须为正整数');
  if (principalCents <= 0) return 0;
  const p = BigInt(principalCents);
  if (rate.num === 0n) {
    return Number(roundDiv(p, BigInt(periods)));
  }
  const n = BigInt(periods);
  const base = rate.den + rate.num;
  const basePow = base ** n;
  const denPow = rate.den ** n;
  const numerator = p * rate.num * basePow;
  const denominator = rate.den * (basePow - denPow);
  return Number(roundDiv(numerator, denominator));
}
