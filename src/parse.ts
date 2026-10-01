// 字面量解析：整数 / 精确小数 / 分数 / 区间（"c±r" 或 "[lo, hi]"）。
// 全部直接解析为 BigInt/有理数，不经过 Number 中转数值本身。
// NaN 与 Infinity 按“不支持”显式拒绝。

import { add, cmp, div, pow10, rational, sub, ZERO } from './rational.ts';
import type { Rational } from './rational.ts';
import { exact, interval } from './value.ts';
import type { CompareValue } from './value.ts';

export class UnsupportedLiteralError extends Error {
  readonly literal: string;
  constructor(literal: string, reason: string) {
    super(`unsupported literal ${JSON.stringify(literal)}: ${reason}`);
    this.name = 'UnsupportedLiteralError';
    this.literal = literal;
  }
}

const MAX_DIGITS = 4096; // 资源保护：位数与指数上限

const FRACTION_RE = /^([+-]?\d+)\/([+-]?\d+)$/;
const DECIMAL_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

export function parseRationalLiteral(text: string): Rational {
  const s = text.trim();
  if (/nan|infinity/i.test(s)) {
    throw new UnsupportedLiteralError(text, 'NaN/Infinity are not supported');
  }
  const frac = FRACTION_RE.exec(s);
  if (frac) {
    return rational(BigInt(frac[1]), BigInt(frac[2]));
  }
  const dec = DECIMAL_RE.exec(s);
  if (dec) {
    const [, sign, intPart, fracPart = '', expPart] = dec;
    if (intPart.length + fracPart.length > MAX_DIGITS) {
      throw new UnsupportedLiteralError(text, 'too many digits');
    }
    // 指数只是“小数点位置”的索引，不是数值本身，用 Number 表示是安全的。
    const exp = expPart === undefined ? 0 : Number(expPart);
    if (!Number.isSafeInteger(exp) || Math.abs(exp) > MAX_DIGITS) {
      throw new UnsupportedLiteralError(text, 'exponent out of range');
    }
    const coeff = BigInt((sign === '-' ? '-' : '') + intPart + fracPart);
    const scale = fracPart.length - exp;
    return scale >= 0 ? rational(coeff, pow10(scale)) : rational(coeff * pow10(-scale), 1n);
  }
  throw new UnsupportedLiteralError(text, 'expected integer, decimal, or fraction literal');
}

export function parseValue(text: string): CompareValue {
  const s = text.trim();
  const pm = s.indexOf('±');
  if (pm >= 0) {
    const center = parseRationalLiteral(s.slice(0, pm));
    const radius = parseRationalLiteral(s.slice(pm + 1));
    if (cmp(radius, ZERO) < 0) {
      throw new UnsupportedLiteralError(text, 'radius must be >= 0');
    }
    return interval(center, radius, `literal:${s}`);
  }
  if (s.startsWith('[') && s.endsWith(']')) {
    const comma = s.indexOf(',');
    if (comma < 0) {
      throw new UnsupportedLiteralError(text, 'interval must be "[lo, hi]"');
    }
    const lo = parseRationalLiteral(s.slice(1, comma));
    const hi = parseRationalLiteral(s.slice(comma + 1, -1));
    if (cmp(lo, hi) > 0) {
      throw new UnsupportedLiteralError(text, 'lower bound above upper bound');
    }
    const two = rational(2n);
    return interval(div(add(lo, hi), two), div(sub(hi, lo), two), `literal:${s}`);
  }
  return exact(parseRationalLiteral(s));
}
