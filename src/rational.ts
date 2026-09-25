/**
 * Exact rational arithmetic on bigint. All scores/weights are rationals so
 * threshold comparisons are exact (no float rounding across a cut line).
 */

export interface Rational {
  readonly num: bigint;
  readonly den: bigint; // always > 0, fraction kept in lowest terms
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

export function rat(num: bigint | number, den: bigint | number = 1n): Rational {
  let n = BigInt(num);
  let d = BigInt(den);
  if (d === 0n) throw new Error('rational with zero denominator');
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

/** Accepted literal forms: integer, "p/q" string, or {num, den}. */
export type WeightSpec = number | string | { num: number | bigint; den?: number | bigint };

export function parseRational(spec: WeightSpec): Rational {
  if (typeof spec === 'number') {
    if (!Number.isInteger(spec)) throw new Error(`weight must be an integer or explicit rational, got ${spec}`);
    return rat(spec);
  }
  if (typeof spec === 'string') {
    const m = /^(-?\d+)\s*(?:\/\s*(\d+))?$/.exec(spec.trim());
    if (!m) throw new Error(`invalid rational literal: ${spec}`);
    return rat(BigInt(m[1]), m[2] ? BigInt(m[2]) : 1n);
  }
  return rat(spec.num, spec.den ?? 1);
}

export const ZERO: Rational = rat(0n);

export function add(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function neg(a: Rational): Rational {
  return { num: -a.num, den: a.den };
}

/** -1, 0, 1 */
export function cmp(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}

export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

export function isZero(a: Rational): boolean {
  return a.num === 0n;
}

export function toString(a: Rational): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}

export function toJSON(a: Rational): string {
  return toString(a);
}
