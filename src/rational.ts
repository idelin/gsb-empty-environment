/**
 * Exact rational arithmetic on BigInt.
 *
 * Rules:
 * - Values are always normalized: den > 0, gcd(|num|, den) = 1.
 * - Parsing is exact: decimal strings are converted digit-by-digit to BigInt.
 *   No Number intermediary is ever used for values (IEEE-754 would lose
 *   precision for large or high-precision decimals).
 * - Rounding happens ONLY at display time, via toDecimalString, with an
 *   explicit rule: round-half-away-from-zero at the requested decimal place.
 *   All internal comparisons (budget checks, bounds) use exact cmp().
 */

export interface Rational {
  readonly num: bigint;
  readonly den: bigint; // always > 0
}

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = absBig(a);
  let y = absBig(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export function makeRational(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new Error('rational: zero denominator');
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

export const ZERO: Rational = { num: 0n, den: 1n };
export const ONE: Rational = { num: 1n, den: 1n };

const MAX_EXPONENT = 100000n; // sanity bound on decimal exponents

/**
 * Parse an exact decimal string: [-]ddd[.ddd][e[-]ddd].
 * No Number conversion is used anywhere; digits go straight into BigInt.
 */
export function parseDecimal(text: string): Rational {
  const s = text.trim();
  const m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s);
  if (!m) throw new Error(`invalid decimal literal: ${JSON.stringify(text)}`);
  const sign = m[1] === '-' ? -1n : 1n;
  const intPart = m[2];
  const fracPart = m[3] ?? '';
  let num = BigInt(intPart + fracPart) * sign;
  let den = 10n ** BigInt(fracPart.length);
  if (m[4] !== undefined) {
    const exp = BigInt(m[4]); // exact; bounded below
    if (exp > MAX_EXPONENT || exp < -MAX_EXPONENT) {
      throw new Error(`decimal exponent out of range: ${m[4]}`);
    }
    if (exp >= 0n) {
      num *= 10n ** exp;
    } else {
      den *= 10n ** -exp;
    }
  }
  return makeRational(num, den);
}

export function add(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.num, a.den * b.den);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new Error('rational: division by zero');
  return makeRational(a.num * b.den, a.den * b.num);
}

export function neg(a: Rational): Rational {
  return { num: -a.num, den: a.den };
}

export function abs(a: Rational): Rational {
  return a.num < 0n ? { num: -a.num, den: a.den } : a;
}

/** Returns -1, 0, 1. Exact cross-multiplication on BigInt. */
export function cmp(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function eq(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

export function isZero(a: Rational): boolean {
  return a.num === 0n;
}

export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}

export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

/**
 * Exact canonical string, e.g. "3/4" or "5". Used inside certificates and
 * hashes so no rounding ever enters the proof artifacts.
 */
export function toExactString(a: Rational): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}

/**
 * Display-only decimal rendering.
 * Rounding rule (explicit): round-half-away-from-zero at `places` decimals.
 * Never used for feasibility or bound decisions.
 */
export function toDecimalString(a: Rational, places: number): string {
  if (!Number.isSafeInteger(places) || places < 0 || places > 100) {
    throw new Error(`invalid display places: ${places}`);
  }
  const scale = 10n ** BigInt(places);
  const negative = a.num < 0n;
  const magnitude = negative ? -a.num : a.num;
  const scaled = magnitude * scale;
  let q = scaled / a.den;
  const rem = scaled % a.den;
  if (rem * 2n >= a.den) q += 1n; // half away from zero
  const intPart = q / scale;
  const fracPart = q % scale;
  if (places === 0) return (negative ? '-' : '') + intPart.toString();
  const frac = fracPart.toString().padStart(places, '0');
  return `${negative ? '-' : ''}${intPart}.${frac}`;
}
