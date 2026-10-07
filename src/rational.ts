/**
 * Exact rational numbers backed by bigint.
 *
 * Invariants: `den > 0` and `gcd(|num|, den) = 1` (fully reduced).
 */
export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

/** Anything that can be converted into a Rational. */
export type RationalLike = Rational | number | string | bigint;

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = absBig(a);
  let y = absBig(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

/** Create a normalized rational. Throws if `den` is zero. */
export function makeRational(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new RangeError('rational denominator must not be zero');
  if (num === 0n) return { num: 0n, den: 1n };
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

export function isRational(value: unknown): value is Rational {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Record<string, unknown>;
  return typeof candidate.num === 'bigint' && typeof candidate.den === 'bigint';
}

const DECIMAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

/** Parse a finite decimal string (optionally with exponent) into an exact rational. */
export function parseDecimalString(text: string): Rational {
  const match = DECIMAL_RE.exec(text.trim());
  if (!match) throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(text)}`);
  const intPart = match[2] ?? '';
  const fracPart = match[3] ?? '';
  if (intPart === '' && fracPart === '') {
    throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(text)}`);
  }
  const digits = (intPart + fracPart).replace(/^0+(?=\d)/, '');
  let num = digits === '' ? 0n : BigInt(digits);
  if (match[1] === '-') num = -num;
  const exp = (match[4] !== undefined ? BigInt(match[4]) : 0n) - BigInt(fracPart.length);
  if (exp >= 0n) return makeRational(num * 10n ** exp, 1n);
  return makeRational(num, 10n ** -exp);
}

/**
 * Convert a double to the rational that exactly equals its binary value.
 * Throws on non-finite input.
 */
export function fromNumberExact(x: number): Rational {
  if (!Number.isFinite(x)) throw new RangeError(`cannot convert ${x} to a rational`);
  if (x === 0) return ZERO;
  const view = new DataView(new ArrayBuffer(8));
  view.setFloat64(0, x);
  const bits = view.getBigUint64(0, false);
  const sign = (bits >> 63n) !== 0n ? -1n : 1n;
  const rawExp = Number((bits >> 52n) & 0x7ffn);
  const frac = bits & ((1n << 52n) - 1n);
  let mantissa: bigint;
  let exp: number;
  if (rawExp === 0) {
    mantissa = frac;
    exp = -1074;
  } else {
    mantissa = frac | (1n << 52n);
    exp = rawExp - 1075;
  }
  const num = sign * (exp >= 0 ? mantissa << BigInt(exp) : mantissa);
  const den = exp >= 0 ? 1n : 1n << BigInt(-exp);
  return makeRational(num, den);
}

/**
 * Coerce a {@link RationalLike} into a Rational.
 *
 * Numbers are converted through their shortest round-tripping decimal
 * representation, so `rational(0.1)` is exactly 1/10. Use
 * {@link fromNumberExact} if you need the exact binary value of a double.
 */
export function rational(value: RationalLike): Rational {
  if (typeof value === 'bigint') return makeRational(value);
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new RangeError(`cannot convert ${value} to a rational`);
    return parseDecimalString(value.toString());
  }
  if (typeof value === 'string') return parseDecimalString(value);
  return value;
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
  if (b.num === 0n) throw new RangeError('rational division by zero');
  return makeRational(a.num * b.den, a.den * b.num);
}

export function neg(a: Rational): Rational {
  return makeRational(-a.num, a.den);
}

export function abs(a: Rational): Rational {
  return a.num < 0n ? makeRational(-a.num, a.den) : a;
}

export function cmp(a: Rational, b: Rational): -1 | 0 | 1 {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function isZero(a: Rational): boolean {
  return a.num === 0n;
}

export function isPositive(a: Rational): boolean {
  return a.num > 0n;
}

export function isNegative(a: Rational): boolean {
  return a.num < 0n;
}

export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}

export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

/** Exact 10^exp as a rational (exp may be negative). */
export function pow10(exp: number): Rational {
  if (!Number.isInteger(exp)) throw new RangeError('pow10 exponent must be an integer');
  return exp >= 0 ? makeRational(10n ** BigInt(exp)) : makeRational(1n, 10n ** BigInt(-exp));
}

/** Approximate a rational as a double. */
export function toNumber(r: Rational): number {
  return Number(r.num) / Number(r.den);
}

/**
 * Exact decimal expansion of `r`, or `null` when the expansion does not
 * terminate (i.e. the denominator has prime factors other than 2 and 5).
 */
export function toDecimalString(r: Rational): string | null {
  let rest = r.den;
  let twos = 0n;
  let fives = 0n;
  while (rest % 2n === 0n) {
    rest /= 2n;
    twos += 1n;
  }
  while (rest % 5n === 0n) {
    rest /= 5n;
    fives += 1n;
  }
  if (rest !== 1n) return null;
  const places = twos > fives ? twos : fives;
  const scaled = r.num * 2n ** (places - twos) * 5n ** (places - fives);
  const negative = scaled < 0n;
  const digits = (negative ? -scaled : scaled).toString();
  const sign = negative ? '-' : '';
  const count = Number(places);
  if (count === 0) return sign + digits;
  const padded = digits.padStart(count + 1, '0');
  const intPart = padded.slice(0, padded.length - count);
  const fracPart = padded.slice(padded.length - count).replace(/0+$/, '');
  return fracPart === '' ? sign + intPart : `${sign}${intPart}.${fracPart}`;
}

/**
 * Canonical exact label for a rational: its terminating decimal expansion
 * when one exists, otherwise the fraction `num/den`. Distinct rationals
 * always produce distinct labels.
 */
export function formatRational(r: Rational): string {
  return toDecimalString(r) ?? `${r.num}/${r.den}`;
}
