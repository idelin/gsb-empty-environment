/**
 * Exact rational arithmetic on BigInt. Values are always stored normalized:
 * denominator > 0 and gcd(|num|, den) = 1. No Number is ever involved, so
 * values of any magnitude or precision remain exact.
 */

function absBigInt(value: bigint): bigint {
  return value < 0n ? -value : value;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = absBigInt(a);
  let y = absBigInt(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export class Rational {
  private constructor(
    readonly num: bigint,
    readonly den: bigint,
  ) {}

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) {
      throw new RangeError('rational denominator must not be zero');
    }
    if (num === 0n) {
      return new Rational(0n, 1n);
    }
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static fromBigInt(value: bigint): Rational {
    return new Rational(value, 1n);
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  get sign(): -1 | 0 | 1 {
    if (this.num === 0n) return 0;
    return this.num < 0n ? -1 : 1;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  add(other: Rational): Rational {
    return Rational.of(
      this.num * other.den + other.num * this.den,
      this.den * other.den,
    );
  }

  sub(other: Rational): Rational {
    return Rational.of(
      this.num * other.den - other.num * this.den,
      this.den * other.den,
    );
  }

  mul(other: Rational): Rational {
    return Rational.of(this.num * other.num, this.den * other.den);
  }

  div(other: Rational): Rational {
    if (other.num === 0n) {
      throw new RangeError('division by zero rational');
    }
    return Rational.of(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(other: Rational): -1 | 0 | 1 {
    const left = this.num * other.den;
    const right = other.num * this.den;
    if (left < right) return -1;
    if (left > right) return 1;
    return 0;
  }

  eq(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  lt(other: Rational): boolean {
    return this.cmp(other) < 0;
  }

  lte(other: Rational): boolean {
    return this.cmp(other) <= 0;
  }

  gt(other: Rational): boolean {
    return this.cmp(other) > 0;
  }

  gte(other: Rational): boolean {
    return this.cmp(other) >= 0;
  }

  /** "3/4" for fractions, "5" for integers. */
  toString(): string {
    if (this.den === 1n) return this.num.toString();
    return `${this.num}/${this.den}`;
  }
}
