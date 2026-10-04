/**
 * 精确有理数：以 BigInt 分子/分母表示，始终保持约分形式且分母为正。
 * 所有运算都是精确的，不存在浮点舍入误差。
 */

export interface Rat {
  readonly num: bigint;
  readonly den: bigint; // 不变式：den > 0 且 gcd(|num|, den) = 1
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

/** 构造约分后的有理数。den 默认为 1。 */
export function rat(num: bigint, den: bigint = 1n): Rat {
  if (den === 0n) throw new Error("rational: zero denominator");
  if (num === 0n) return ZERO;
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

export const ZERO: Rat = Object.freeze({ num: 0n, den: 1n });
export const ONE: Rat = Object.freeze({ num: 1n, den: 1n });

export function add(a: Rat, b: Rat): Rat {
  return rat(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function neg(a: Rat): Rat {
  if (a.num === 0n) return ZERO;
  return { num: -a.num, den: a.den };
}

export function sub(a: Rat, b: Rat): Rat {
  return rat(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rat, b: Rat): Rat {
  return rat(a.num * b.num, a.den * b.den);
}

export function div(a: Rat, b: Rat): Rat {
  if (b.num === 0n) throw new Error("rational: division by zero");
  return rat(a.num * b.den, a.den * b.num);
}

/** a < b 返回 -1，a == b 返回 0，a > b 返回 1。 */
export function cmp(a: Rat, b: Rat): number {
  const l = a.num * b.den;
  const r = b.num * a.den;
  return l < r ? -1 : l > r ? 1 : 0;
}

export function isZero(a: Rat): boolean {
  return a.num === 0n;
}

export function isPositive(a: Rat): boolean {
  return a.num > 0n;
}

export function isNegative(a: Rat): boolean {
  return a.num < 0n;
}

export function sum(terms: readonly Rat[]): Rat {
  let acc = ZERO;
  for (const t of terms) acc = add(acc, t);
  return acc;
}

/** 解析 "3"、"-7"、"22/7"、"-3/4" 形式的字符串。 */
export function parseRat(text: string): Rat {
  const s = text.trim();
  const m = /^([+-]?\d+)(?:\/([+-]?\d+))?$/.exec(s);
  if (!m) throw new Error(`invalid rational literal: ${JSON.stringify(text)}`);
  return rat(BigInt(m[1]), m[2] !== undefined ? BigInt(m[2]) : 1n);
}

/** 输出约分形式：整数输出 "3"，分数输出 "-22/7"。 */
export function formatRat(a: Rat): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}
