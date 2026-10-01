// 精确有理数：以 BigInt 分子/分母表示，全程不经过 Number。
// 不变式：den > 0 且 gcd(|num|, den) = 1（构造时规范化）。

export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export function rational(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new RangeError('rational: denominator must not be zero');
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

export const ZERO: Rational = Object.freeze({ num: 0n, den: 1n });
export const ONE: Rational = Object.freeze({ num: 1n, den: 1n });

export function isZero(r: Rational): boolean {
  return r.num === 0n;
}

export function neg(r: Rational): Rational {
  return { num: -r.num, den: r.den };
}

export function abs(r: Rational): Rational {
  return r.num < 0n ? { num: -r.num, den: r.den } : r;
}

export function add(a: Rational, b: Rational): Rational {
  return rational(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return rational(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rational, b: Rational): Rational {
  return rational(a.num * b.num, a.den * b.den);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new RangeError('div: division by zero');
  return rational(a.num * b.den, a.den * b.num);
}

export function cmp(a: Rational, b: Rational): -1 | 0 | 1 {
  const left = a.num * b.den;
  const right = b.num * a.den;
  return left < right ? -1 : left > right ? 1 : 0;
}

export function equal(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

export function pow10(exp: number): bigint {
  if (!Number.isInteger(exp) || exp < 0) {
    throw new RangeError('pow10: exponent must be a non-negative integer');
  }
  return 10n ** BigInt(exp);
}

export function toString(r: Rational): string {
  return r.den === 1n ? r.num.toString() : `${r.num}/${r.den}`;
}
