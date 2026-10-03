/**
 * Exact rational arithmetic on BigInt. Values never pass through Number,
 * so arbitrarily large magnitudes keep full precision.
 *
 * Invariant: den > 0 and gcd(|num|, den) = 1.
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

export function lcm(a: bigint, b: bigint): bigint {
  return (a / gcd(a, b)) * b;
}

export function rat(num: bigint, den: bigint = 1n): Rational {
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

/**
 * Parses an exact decimal or fraction string into a Rational.
 * Accepted forms: "3", "-2", "0.125", ".5", "3/4", "-1.5/2".
 * Digits are assembled directly into BigInt; Number is never involved,
 * so "123456789012345678901234567890.1" parses exactly.
 */
export function parseRational(text: string): Rational {
  const s = text.trim();
  if (s.length === 0) throw new Error('rational: empty input');
  const slash = s.indexOf('/');
  if (slash >= 0) {
    if (s.indexOf('/', slash + 1) >= 0) {
      throw new Error(`rational: too many '/': ${text}`);
    }
    const n = parseDecimal(s.slice(0, slash));
    const d = parseDecimal(s.slice(slash + 1));
    if (d.den !== 1n) {
      throw new Error(`rational: denominator must be an integer: ${text}`);
    }
    return divR(n, rat(d.num));
  }
  return parseDecimal(s);
}

function parseDecimal(s: string): Rational {
  const m = /^([+-]?)(\d*)(?:\.(\d+))?$/.exec(s);
  if (!m || (m[2] === '' && m[3] === undefined)) {
    throw new Error(`rational: invalid decimal: ${s}`);
  }
  const intPart = m[2] === '' ? '0' : m[2];
  const fracPart = m[3] ?? '';
  let num = BigInt(intPart + fracPart);
  if (m[1] === '-') num = -num;
  const den = 10n ** BigInt(fracPart.length);
  return rat(num, den);
}

export function addR(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function subR(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mulR(a: Rational, b: Rational): Rational {
  return rat(a.num * b.num, a.den * b.den);
}

export function divR(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new Error('rational: division by zero');
  return rat(a.num * b.den, a.den * b.num);
}

/** Returns -1, 0 or 1. */
export function cmpR(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  if (lhs < rhs) return -1;
  if (lhs > rhs) return 1;
  return 0;
}

export function isNegR(a: Rational): boolean {
  return a.num < 0n;
}

export function formatR(a: Rational): string {
  if (a.den === 1n) return a.num.toString();
  return `${a.num}/${a.den}`;
}
