/**
 * 精确有理数（基于 BigInt），不经过 Number 中转。
 * 不变式：den > 0，gcd(|num|, den) = 1，0 表示为 0/1。
 */
export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

/** 显式舍入规则（用于把有理数格式化为有限小数时）。 */
export type RoundingMode =
  | 'down'      // 向零截断
  | 'up'        // 远离零
  | 'floor'     // 向负无穷
  | 'ceil'      // 向正无穷
  | 'half-up'   // 四舍五入（|余数|*2 >= 分母 时远离零）
  | 'half-even';// 银行家舍入（恰好一半时取偶）

export function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export function makeRational(num: bigint, den: bigint): Rational {
  if (den === 0n) throw new Error('denominator must not be zero');
  if (den < 0n) { num = -num; den = -den; }
  if (num === 0n) return { num: 0n, den: 1n };
  const g = gcd(num, den);
  return { num: num / g, den: den / g };
}

export const ZERO: Rational = { num: 0n, den: 1n };

export function add(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.num, a.den * b.den);
}

export function divBig(a: Rational, n: bigint): Rational {
  return makeRational(a.num, a.den * n);
}

export function eq(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

const DECIMAL_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

/**
 * 把十进制字符串 / bigint 解析为精确有理数。
 * 支持 "123"、"-0.001"、"1.25e3"、"6.02e-23" 等形式。
 * 大数必须以 string 或 bigint 传入，绝不经过 Number。
 */
export function parseDecimal(input: string | bigint): Rational {
  if (typeof input === 'bigint') return { num: input, den: 1n };
  const m = DECIMAL_RE.exec(input.trim());
  if (!m) throw new Error(`invalid decimal literal: ${JSON.stringify(input)}`);
  const [, sign, intPart, fracPart = '', expPart] = m;
  const digits = (intPart + fracPart).replace(/^0+(?=\d)/, '');
  let num = BigInt(digits === '' ? '0' : digits);
  if (sign === '-') num = -num;
  let scale = BigInt(fracPart.length);
  if (expPart !== undefined) scale -= BigInt(expPart);
  if (scale >= 0n) return makeRational(num, 10n ** scale);
  return makeRational(num * 10n ** (-scale), 1n);
}

/** 按显式舍入规则，把有理数格式化为 places 位小数的十进制字符串。 */
export function toDecimal(r: Rational, places: number, mode: RoundingMode = 'half-up'): string {
  if (!Number.isInteger(places) || places < 0) {
    throw new Error('places must be a non-negative integer');
  }
  const scale = 10n ** BigInt(places);
  const scaled = r.num * scale;
  let q = scaled / r.den;          // BigInt 除法：向零截断
  const rem = scaled % r.den;      // 与 q 同号（或被零）
  if (rem !== 0n) {
    const absRem = rem < 0n ? -rem : rem;
    let bump = false;
    switch (mode) {
      case 'down': bump = false; break;
      case 'up': bump = true; break;
      case 'floor': bump = scaled < 0n; break;
      case 'ceil': bump = scaled > 0n; break;
      case 'half-up': bump = absRem * 2n >= r.den; break;
      case 'half-even': {
        const twice = absRem * 2n;
        bump = twice > r.den || (twice === r.den && (q % 2n !== 0n));
        break;
      }
    }
    if (bump) q += scaled < 0n ? -1n : 1n;
  }
  const neg = q < 0n;
  const abs = neg ? -q : q;
  const s = abs.toString().padStart(places + 1, '0');
  const head = places === 0 ? s : s.slice(0, -places);
  const tail = places === 0 ? '' : '.' + s.slice(-places);
  return (neg ? '-' : '') + head + tail;
}

/** 精确分数形式，如 "-3/2"；分母为 1 时只输出分子。 */
export function toFraction(r: Rational): string {
  return r.den === 1n ? r.num.toString() : `${r.num}/${r.den}`;
}
