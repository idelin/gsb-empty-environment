// Exact rational arithmetic backed by BigInt.
//
// Design rules:
// - Quantities are never routed through Number. Decimal strings are parsed
//   directly into BigInt numerator/denominator, so values beyond 2^53 stay
//   exact. `number` inputs are accepted only as a convenience and are
//   converted via their shortest exact string form (String(n)); use strings
//   or bigint for large or high-precision values.
// - All arithmetic (+ - * /) is exact. The only places rounding can occur
//   are (a) square roots and (b) decimal formatting, and both take an
//   explicit RoundingMode.

export type RoundingMode = 'half-even' | 'half-up' | 'down' | 'up';
export type RationalInput = Rational | string | bigint | number;

export const DEFAULT_ROUNDING: RoundingMode = 'half-even';
export const DEFAULT_STD_DECIMALS = 6;

const DECIMAL_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcd(a: bigint, b: bigint): bigint {
  a = absBig(a);
  b = absBig(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

// Round the exact rational n/d (d > 0) to an integer per the rounding mode.
// 'down' = toward zero, 'up' = away from zero, 'half-up' = ties away from
// zero, 'half-even' = ties to even (banker's rounding).
function roundQuotient(n: bigint, d: bigint, mode: RoundingMode): bigint {
  const q = n / d; // truncates toward zero
  const r = n % d; // carries the sign of n
  if (r === 0n) return q;
  const away = n > 0n ? q + 1n : q - 1n;
  switch (mode) {
    case 'down':
      return q;
    case 'up':
      return away;
    case 'half-up':
    case 'half-even': {
      const twice = 2n * absBig(r);
      if (twice > d) return away;
      if (twice < d) return q;
      if (mode === 'half-up') return away;
      return q % 2n === 0n ? q : away;
    }
  }
}

export class Rational {
  static readonly zero = new Rational(0n, 1n);
  static readonly one = new Rational(1n, 1n);

  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    if (den === 0n) throw new Error('Rational: zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static of(input: RationalInput): Rational {
    if (input instanceof Rational) return input;
    if (typeof input === 'bigint') return new Rational(input, 1n);
    if (typeof input === 'number') {
      if (!Number.isFinite(input)) {
        throw new Error(`Rational: non-finite number ${input}`);
      }
      // Exact shortest string form of the double; never used for big values.
      return Rational.fromDecimalString(String(input));
    }
    return Rational.fromDecimalString(input);
  }

  static fromIntegers(num: bigint, den: bigint): Rational {
    return new Rational(num, den);
  }

  static fromDecimalString(text: string): Rational {
    const m = DECIMAL_RE.exec(text.trim());
    if (!m) throw new Error(`Invalid decimal literal: ${JSON.stringify(text)}`);
    const [, sign, intPart, fracPart = '', expPart] = m;
    let num = BigInt(intPart + fracPart);
    if (sign === '-') num = -num;
    const exp = (expPart !== undefined ? BigInt(expPart) : 0n) - BigInt(fracPart.length);
    if (exp >= 0n) return new Rational(num * 10n ** exp, 1n);
    return new Rational(num, 10n ** -exp);
  }

  add(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.num, this.den * o.den);
  }

  div(other: RationalInput): Rational {
    const o = Rational.of(other);
    if (o.num === 0n) throw new Error('Rational: division by zero');
    return new Rational(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  square(): Rational {
    return new Rational(this.num * this.num, this.den * this.den);
  }

  cmp(other: RationalInput): number {
    const o = Rational.of(other);
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  sign(): number {
    return this.num === 0n ? 0 : this.num > 0n ? 1 : -1;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }

  // Exact decimal rendering to `places` fractional digits under `mode`.
  toDecimal(places: number, mode: RoundingMode = DEFAULT_ROUNDING): string {
    if (!Number.isInteger(places) || places < 0) {
      throw new Error('toDecimal: places must be a non-negative integer');
    }
    const scale = 10n ** BigInt(places);
    const q = roundQuotient(this.num * scale, this.den, mode);
    const neg = q < 0n;
    const a = absBig(q);
    const intPart = a / scale;
    if (places === 0) return (neg ? '-' : '') + intPart.toString();
    const frac = (a % scale).toString().padStart(places, '0');
    return `${neg ? '-' : ''}${intPart}.${frac}`;
  }
}

// Floor of the integer square root of n >= 0 (Newton's method on BigInt).
export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new Error('isqrt: negative input');
  if (n < 2n) return n;
  let x = n;
  let y = (x + 1n) >> 1n;
  while (y < x) {
    x = y;
    y = (x + n / x) >> 1n;
  }
  return x;
}

// sqrt(r) rounded to `places` decimal digits under `mode`. The comparison
// against the rounding boundary is exact (BigInt), so the returned rational
// is the correctly rounded value of the true square root.
export function sqrtRational(
  r: Rational,
  places: number,
  mode: RoundingMode = DEFAULT_ROUNDING,
): Rational {
  if (r.sign() < 0) throw new Error(`sqrt of negative rational ${r.toString()}`);
  if (!Number.isInteger(places) || places < 0) {
    throw new Error('sqrtRational: places must be a non-negative integer');
  }
  const scale = 10n ** BigInt(places);
  // sqrt(num/den) * scale = sqrt(num * scale^2 / den)
  const a = r.num * scale * scale;
  const b = r.den;
  const s = isqrt(a / b); // floor: s^2 * b <= a < (s+1)^2 * b
  let rounded: bigint;
  if (s * s * b === a) {
    rounded = s; // exact square
  } else if (mode === 'down') {
    rounded = s;
  } else if (mode === 'up') {
    rounded = s + 1n;
  } else {
    // Compare a/b with (s + 1/2)^2 exactly: 4a  ?  (2s+1)^2 * b
    const lhs = 4n * a;
    const rhs = (2n * s + 1n) * (2n * s + 1n) * b;
    if (lhs < rhs) {
      rounded = s;
    } else if (lhs > rhs) {
      rounded = s + 1n;
    } else if (mode === 'half-up') {
      rounded = s + 1n;
    } else {
      rounded = s % 2n === 0n ? s : s + 1n;
    }
  }
  return Rational.fromIntegers(rounded, scale);
}
