// 精确十进制：value = coeff × 10^(-scale)，系数为 BigInt。
// 除法必须显式指定舍入规则（RoundingMode），不存在隐式舍入。

import { pow10, rational } from './rational.ts';
import type { Rational } from './rational.ts';

// floor     向负无穷舍入
// ceil      向正无穷舍入
// truncate  向零舍入
// half-up   最近舍入，恰为一半时远离零（round half away from zero）
// half-even 最近舍入，恰为一半时取偶数（银行家舍入）
export type RoundingMode = 'floor' | 'ceil' | 'truncate' | 'half-up' | 'half-even';

export interface Decimal {
  readonly coeff: bigint;
  readonly scale: number;
}

export function decimal(coeff: bigint, scale: number): Decimal {
  if (!Number.isInteger(scale) || scale < 0) {
    throw new RangeError('decimal: scale must be a non-negative integer');
  }
  return { coeff, scale };
}

// 计算 num/den 并按要求舍入到 10^(-scale)。余数比较全部在 BigInt 上进行。
export function divideDecimal(num: bigint, den: bigint, scale: number, mode: RoundingMode): Decimal {
  if (den === 0n) throw new RangeError('divideDecimal: denominator must not be zero');
  if (!Number.isInteger(scale) || scale < 0) {
    throw new RangeError('divideDecimal: scale must be a non-negative integer');
  }
  const scaled = num * pow10(scale);
  let q = scaled / den; // BigInt 除法向零截断
  const r = scaled % den;
  if (r !== 0n) {
    const absR = r < 0n ? -r : r;
    const absD = den < 0n ? -den : den;
    const sign: bigint = (r > 0n) === (den > 0n) ? 1n : -1n; // 真实余数的符号
    switch (mode) {
      case 'truncate':
        break;
      case 'floor':
        if (sign < 0n) q -= 1n;
        break;
      case 'ceil':
        if (sign > 0n) q += 1n;
        break;
      case 'half-up':
        if (2n * absR >= absD) q += sign;
        break;
      case 'half-even': {
        const twice = 2n * absR;
        if (twice > absD || (twice === absD && q % 2n !== 0n)) q += sign;
        break;
      }
    }
  }
  return { coeff: q, scale };
}

export function decimalToRational(d: Decimal): Rational {
  return rational(d.coeff, pow10(d.scale));
}

export function addDecimal(a: Decimal, b: Decimal): Decimal {
  const scale = Math.max(a.scale, b.scale);
  const ca = a.coeff * pow10(scale - a.scale);
  const cb = b.coeff * pow10(scale - b.scale);
  return { coeff: ca + cb, scale };
}

export function decimalToString(d: Decimal): string {
  const negative = d.coeff < 0n;
  const digits = (negative ? -d.coeff : d.coeff).toString().padStart(d.scale + 1, '0');
  const intPart = digits.slice(0, digits.length - d.scale);
  if (d.scale === 0) return (negative ? '-' : '') + intPart;
  const fracPart = digits.slice(digits.length - d.scale);
  return `${negative ? '-' : ''}${intPart}.${fracPart}`;
}
