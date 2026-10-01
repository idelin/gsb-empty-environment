/**
 * Exact rational arithmetic on BigInt.
 *
 * A Rational is always kept normalized: den > 0 and gcd(|num|, den) = 1.
 * No value ever passes through Number, so quantities beyond 2^53 stay exact.
 */

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
  if (den === 0n) {
    throw new RangeError('rational: denominator must not be zero');
  }
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

export function fromInt(value: bigint): Rational {
  return rational(value, 1n);
}

export const ZERO: Rational = rational(0n, 1n);
export const ONE: Rational = rational(1n, 1n);
export const HUNDRED: Rational = rational(100n, 1n);

export function add(a: Rational, b: Rational): Rational {
  return rational(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function neg(a: Rational): Rational {
  return rational(-a.num, a.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return rational(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rational, b: Rational): Rational {
  return rational(a.num * b.num, a.den * b.den);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) {
    throw new RangeError('rational: division by zero');
  }
  return rational(a.num * b.den, a.den * b.num);
}

export function abs(a: Rational): Rational {
  return a.num < 0n ? neg(a) : a;
}

export function isZero(a: Rational): boolean {
  return a.num === 0n;
}

export function sign(a: Rational): -1 | 0 | 1 {
  if (a.num > 0n) return 1;
  if (a.num < 0n) return -1;
  return 0;
}

export function cmp(a: Rational, b: Rational): -1 | 0 | 1 {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  if (lhs < rhs) return -1;
  if (lhs > rhs) return 1;
  return 0;
}

export function equal(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

/** Exact "num/den" rendering, reduced. No rounding involved. */
export function toExactString(a: Rational): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}
