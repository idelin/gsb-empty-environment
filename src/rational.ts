/**
 * Exact rational arithmetic on BigInt.
 *
 * Invariants: den > 0, gcd(|num|, den) === 1, zero is 0/1.
 * No value ever passes through Number, so arbitrarily large
 * integers and long decimals stay exact.
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

export function make(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new RangeError('rational with zero denominator');
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

export function fromBigInt(value: bigint): Rational {
  return { num: value, den: 1n };
}

/** Exact decimal construction: value = mantissa * 10^exp, reduced. */
export function fromScaledInt(mantissa: bigint, exp: number): Rational {
  if (exp >= 0) return make(mantissa * 10n ** BigInt(exp), 1n);
  return make(mantissa, 10n ** BigInt(-exp));
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

export function mul(a: Rational, b: Rational): Rational {
  return make(a.num * b.num, a.den * b.den);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new RangeError('division by zero rational');
  return make(a.num * b.den, a.den * b.num);
}

/** -1, 0, 1 — exact three-way comparison, no epsilon involved. */
export function cmp(a: Rational, b: Rational): -1 | 0 | 1 {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function isZero(a: Rational): boolean {
  return a.num === 0n;
}

export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}

/**
 * Explicit rounding modes. There is no default: every decimal
 * materialisation of a rational must name its rule.
 */
export type RoundingMode = 'floor' | 'ceil' | 'trunc' | 'half-up' | 'half-even';

/** Round `a` to an integer multiple of 10^-scale under an explicit mode. */
export function roundToScale(a: Rational, scale: number, mode: RoundingMode): Rational {
  if (!Number.isInteger(scale) || scale < 0) {
    throw new RangeError('scale must be a non-negative integer');
  }
  const factor = 10n ** BigInt(scale);
  const scaled = a.num * factor; // value * 10^scale = scaled / den
  const q = scaled / a.den; // BigInt division truncates toward zero
  const r = scaled % a.den;
  if (r === 0n) return make(q, factor);

  const twiceAbsR = (r < 0n ? -r : r) * 2n;
  const away = q >= 0n ? q + 1n : q - 1n; // one unit away from zero
  switch (mode) {
    case 'trunc':
      return make(q, factor);
    case 'floor':
      return make(r > 0n ? q : q - 1n, factor);
    case 'ceil':
      return make(r > 0n ? q + 1n : q, factor);
    case 'half-up': {
      // ties go away from zero
      return make(twiceAbsR >= a.den ? away : q, factor);
    }
    case 'half-even': {
      if (twiceAbsR > a.den) return make(away, factor);
      if (twiceAbsR < a.den) return make(q, factor);
      return make(q % 2n === 0n ? q : away, factor);
    }
  }
}

/** Exact decimal string when the denominator only has factors 2 and 5. */
export function toTerminatingDecimal(a: Rational): string | null {
  let d = a.den;
  let twos = 0;
  let fives = 0;
  while (d % 2n === 0n) {
    d /= 2n;
    twos++;
  }
  while (d % 5n === 0n) {
    d /= 5n;
    fives++;
  }
  if (d !== 1n) return null;
  const scale = Math.max(twos, fives);
  const scaled = a.num * 2n ** BigInt(scale - twos) * 5n ** BigInt(scale - fives);
  const digits = (scaled < 0n ? -scaled : scaled).toString().padStart(scale + 1, '0');
  const intPart = digits.slice(0, digits.length - scale);
  const fracPart = scale === 0 ? '' : '.' + digits.slice(digits.length - scale);
  return (scaled < 0n ? '-' : '') + intPart + fracPart;
}

/**
 * Canonical string: exact terminating decimal when possible,
 * otherwise "num/den". Never an approximation.
 */
export function toString(a: Rational): string {
  const dec = toTerminatingDecimal(a);
  return dec !== null ? dec : `${a.num}/${a.den}`;
}

/** Decimal string at a fixed scale under an explicit rounding mode. */
export function toDecimalString(a: Rational, scale: number, mode: RoundingMode): string {
  const rounded = roundToScale(a, scale, mode);
  const dec = toTerminatingDecimal(rounded);
  if (dec === null) throw new Error('internal: rounded value must be decimal');
  return dec;
}
