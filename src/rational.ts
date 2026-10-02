/**
 * Exact rational arithmetic on BigInt. No Number is ever used to carry a
 * value: every quantity is a normalized { num, den } pair with den > 0 and
 * gcd(|num|, den) = 1 (zero is always 0/1).
 */

export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

/** Normalize to lowest terms with a positive denominator. */
export function make(num: bigint, den: bigint): Rational {
  if (den === 0n) throw new Error("rational with zero denominator");
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  if (n === 0n) return { num: 0n, den: 1n };
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

export const ZERO: Rational = { num: 0n, den: 1n };

export function fromBigInt(n: bigint): Rational {
  return { num: n, den: 1n };
}

export function neg(a: Rational): Rational {
  return { num: -a.num, den: a.den };
}

export function abs(a: Rational): Rational {
  return a.num < 0n ? { num: -a.num, den: a.den } : a;
}

export function add(a: Rational, b: Rational): Rational {
  return make(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return make(a.num * b.den - b.num * a.den, a.den * b.den);
}

/** -1 / 0 / +1 comparison, exact via cross-multiplication. */
export function cmp(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function eq(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

export function isInteger(a: Rational): boolean {
  return a.den === 1n;
}

/** Truncating (toward zero) floor is NOT what we want for negatives: this is a true floor. */
export function floor(a: Rational): bigint {
  const q = a.num / a.den;
  return a.num % a.den !== 0n && a.num < 0n ? q - 1n : q;
}

export function ceil(a: Rational): bigint {
  return -floor({ num: -a.num, den: a.den });
}

/**
 * Parse an exact decimal / integer / fraction string into a Rational.
 * Accepted forms: "3", "-3", "3.25", "-0.001", "22/7", "-22/7", "+4.5".
 * Digits are consumed directly as characters into a BigInt; nothing passes
 * through Number, so arbitrarily many digits keep full precision.
 */
export function parseRational(input: string): Rational {
  const s = input.trim();
  const slash = s.indexOf("/");
  if (slash >= 0) {
    const n = parseRational(s.slice(0, slash));
    const d = parseRational(s.slice(slash + 1));
    if (!isInteger(n) || !isInteger(d)) {
      throw new Error(`fraction parts must be integers: ${input}`);
    }
    return make(n.num, d.num);
  }
  const m = /^([+-]?)(\d*)(?:\.(\d+))?$/.exec(s);
  if (!m || (m[2] === "" && m[3] === undefined)) {
    throw new Error(`not an exact decimal: ${input}`);
  }
  const sign = m[1] === "-" ? -1n : 1n;
  const intPart = m[2] === "" ? 0n : BigInt(m[2]);
  const fracPart = m[3] ?? "";
  const scale = 10n ** BigInt(fracPart.length);
  const frac = fracPart === "" ? 0n : BigInt(fracPart);
  return make(sign * (intPart * scale + frac), scale);
}

/** Exact decimal expansion with `digits` places after the point (truncated, never rounded). */
export function toDecimal(a: Rational, digits = 10): string {
  if (digits < 0) throw new Error("digits must be >= 0");
  const scale = 10n ** BigInt(digits);
  const scaled = (abs(a).num * scale) / a.den;
  const intPart = scaled / scale;
  const fracPart = (scaled % scale).toString().padStart(digits, "0");
  const body = digits === 0 ? intPart.toString() : `${intPart}.${fracPart}`;
  return a.num < 0n ? `-${body}` : body;
}

export function toString(a: Rational): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}
