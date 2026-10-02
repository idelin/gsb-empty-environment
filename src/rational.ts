/**
 * Exact rational arithmetic on BigInt.
 *
 * Every calibration value is parsed directly from its decimal string into a
 * Rational (mantissa / 10^k) and all interpolation math stays in exact
 * rational form. Number is never used to carry a value; it only appears for
 * the (bounded) decimal exponent and scale bookkeeping.
 */

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

const MAX_EXPONENT = 1_000_000;

export class Rational {
  readonly num: bigint; // sign is carried by num
  readonly den: bigint; // always > 0, always reduced

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error('rational denominator is zero');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    return new Rational(num / g, den / g);
  }

  static fromBigInt(value: bigint): Rational {
    return new Rational(value, 1n);
  }

  /**
   * Parses a decimal literal such as "-12.340e2" without ever routing the
   * digits through Number, so arbitrarily large/small values stay exact.
   */
  static parse(text: string): Rational {
    const trimmed = text.trim();
    const m = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(trimmed);
    const intPart = m?.[2] ?? '';
    const fracPart = m?.[3] ?? '';
    if (!m || (intPart === '' && fracPart === '')) {
      throw new Error(`invalid decimal literal: ${JSON.stringify(text)}`);
    }
    const exponentText = m[4];
    let exponent = 0;
    if (exponentText !== undefined) {
      exponent = Number(exponentText); // exponent magnitude only, never the value
      if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > MAX_EXPONENT) {
        throw new Error(`decimal exponent out of supported range: ${exponentText}`);
      }
    }
    const digits = intPart + fracPart;
    let mantissa = BigInt(digits === '' ? '0' : digits);
    if (m[1] === '-') mantissa = -mantissa;
    const scale = fracPart.length - exponent;
    if (scale <= 0) return Rational.of(mantissa * 10n ** BigInt(-scale));
    return Rational.of(mantissa, 10n ** BigInt(scale));
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
    if (other.num === 0n) throw new Error('division by zero');
    return Rational.of(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  compare(other: Rational): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  /**
   * Renders as a plain decimal string when the value has a terminating
   * decimal expansion, otherwise as a canonical "num/den" fraction so the
   * exact value is never silently rounded.
   */
  toDecimalString(): string {
    let d = this.den;
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
    if (d !== 1n) return `${this.num}/${this.den}`;
    const scale = Math.max(twos, fives);
    const scaled = this.num * 2n ** BigInt(scale - twos) * 5n ** BigInt(scale - fives);
    return formatScaledInteger(scaled, scale);
  }

  toString(): string {
    return this.toDecimalString();
  }
}

/** Formats q * 10^(-scale) as a plain decimal string (scale may be negative). */
export function formatScaledInteger(q: bigint, scale: number): string {
  const negative = q < 0n;
  const digits = (negative ? -q : q).toString();
  let body: string;
  if (scale <= 0) {
    body = digits + '0'.repeat(-scale);
  } else {
    const padded = digits.padStart(scale + 1, '0');
    body = `${padded.slice(0, -scale)}.${padded.slice(-scale)}`;
  }
  return negative && q !== 0n ? `-${body}` : body;
}
