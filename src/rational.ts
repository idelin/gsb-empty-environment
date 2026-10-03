/**
 * Exact rational number arithmetic backed by BigInt.
 * All values are kept in canonical form: gcd(|num|, |den|) = 1, den > 0.
 */

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(value: bigint | number | string | Rational): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return new Rational(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isInteger(value)) {
        throw new TypeError(`Non-integer number ${value} cannot be made exact; use a string like "p/q"`);
      }
      return new Rational(BigInt(value), 1n);
    }
    const s = value.trim();
    const slash = s.indexOf('/');
    if (slash === -1) return new Rational(BigInt(s), 1n);
    return Rational.make(BigInt(s.slice(0, slash)), BigInt(s.slice(slash + 1)));
  }

  /** Build and normalize p/q. */
  static make(num: bigint, den: bigint): Rational {
    if (den === 0n) throw new RangeError('Division by zero: denominator is 0');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    if (n === 0n) return new Rational(0n, 1n);
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  isZero(): boolean {
    return this.num === 0n;
  }

  isOne(): boolean {
    return this.num === 1n && this.den === 1n;
  }

  add(other: Rational): Rational {
    return Rational.make(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rational): Rational {
    return Rational.make(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rational): Rational {
    if (this.isZero() || other.isZero()) return Rational.ZERO;
    return Rational.make(this.num * other.num, this.den * other.den);
  }

  div(other: Rational): Rational {
    if (other.isZero()) throw new RangeError('Division by zero rational');
    return Rational.make(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? new Rational(-this.num, this.den) : this;
  }

  sign(): -1 | 0 | 1 {
    if (this.num === 0n) return 0;
    return this.num > 0n ? 1 : -1;
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}

export function r(value: bigint | number | string | Rational): Rational {
  return Rational.of(value);
}
