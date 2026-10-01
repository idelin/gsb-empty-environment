/**
 * Exact rational numbers backed by BigInt.
 *
 * Values are always stored normalized: denominator > 0, gcd(|num|, den) = 1.
 * No Number is ever used to carry a value; Number appears only for the
 * `places` parameter (a count of digits, not a magnitude).
 */

export const RoundingMode = {
  /** Round half to even (banker's rounding). */
  HalfEven: 'half-even',
  /** Round half away from zero. */
  HalfUp: 'half-up',
  /** Round half toward zero. */
  HalfDown: 'half-down',
  /** Away from zero. */
  Up: 'up',
  /** Toward zero (truncation). */
  Down: 'down',
  /** Toward negative infinity. */
  Floor: 'floor',
  /** Toward positive infinity. */
  Ceiling: 'ceiling',
} as const;

export type RoundingMode = (typeof RoundingMode)[keyof typeof RoundingMode];

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

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new RangeError('rational denominator must not be zero');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static from(value: bigint | number | string | Rational): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return Rational.of(value);
    if (typeof value === 'string') return Rational.parse(value);
    if (!Number.isSafeInteger(value)) {
      throw new RangeError(
        `refusing to convert non-safe-integer Number ${value} to Rational; pass a bigint or decimal string instead`,
      );
    }
    return Rational.of(BigInt(value));
  }

  /**
   * Exact parse of a decimal literal: optional sign, digits, one decimal
   * point, optional base-10 exponent. No binary floating point involved.
   * Examples: "273.15", "-0.001", "1.25e3", "6.02214076e23".
   */
  static parse(text: string): Rational {
    const m = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m || (m[2] === '' && (m[3] === undefined || m[3] === ''))) {
      throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(text)}`);
    }
    const sign = m[1] === '-' ? -1n : 1n;
    const intPart = m[2] === '' ? '0' : m[2];
    const fracPart = m[3] ?? '';
    const digits = BigInt(intPart + fracPart);
    let exponent = BigInt(m[4] ?? '0') - BigInt(fracPart.length);
    let num = sign * digits;
    let den = 1n;
    if (exponent >= 0n) {
      num *= 10n ** exponent;
    } else {
      den = 10n ** -exponent;
    }
    return Rational.of(num, den);
  }

  add(other: Rational): Rational {
    return Rational.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rational): Rational {
    return Rational.of(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rational): Rational {
    return Rational.of(this.num * other.num, this.den * other.den);
  }

  div(other: Rational): Rational {
    if (other.num === 0n) throw new RangeError('division by zero rational');
    return Rational.of(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  compare(other: Rational): -1 | 0 | 1 {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  /** Exact "num/den" (or plain integer) form. Never approximates. */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * Decimal rendering with an explicit rounding rule. The caller must choose
   * both the number of fractional digits and the rounding mode; there is no
   * silent default rounding anywhere in this library.
   */
  toDecimalString(places: number, mode: RoundingMode): string {
    if (!Number.isSafeInteger(places) || places < 0) {
      throw new RangeError('places must be a non-negative safe integer');
    }
    const scale = 10n ** BigInt(places);
    const scaled = this.num * scale;
    let q = scaled / this.den; // truncates toward zero
    const r = scaled % this.den;
    if (r !== 0n) {
      const away = r > 0n ? 1n : -1n; // one unit in the last place, away from zero
      switch (mode) {
        case RoundingMode.Down:
          break;
        case RoundingMode.Up:
          q += away;
          break;
        case RoundingMode.Floor:
          if (r < 0n) q -= 1n;
          break;
        case RoundingMode.Ceiling:
          if (r > 0n) q += 1n;
          break;
        case RoundingMode.HalfUp:
        case RoundingMode.HalfDown:
        case RoundingMode.HalfEven: {
          const twice = (r < 0n ? -r : r) * 2n;
          if (twice > this.den) {
            q += away;
          } else if (twice === this.den) {
            if (mode === RoundingMode.HalfUp) q += away;
            else if (mode === RoundingMode.HalfEven && q % 2n !== 0n) q += away;
            // HalfDown keeps the truncated quotient on a tie.
          }
          break;
        }
      }
    }
    const negative = q < 0n;
    const digits = (negative ? -q : q).toString().padStart(places + 1, '0');
    const head = places === 0 ? digits : digits.slice(0, -places);
    const tail = places === 0 ? '' : `.${digits.slice(-places)}`;
    return `${negative ? '-' : ''}${head}${tail}`;
  }
}
