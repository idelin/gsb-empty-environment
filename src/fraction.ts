/**
 * Exact rational number backed by BigInt, always kept in normalized form
 * (denominator > 0, gcd(|num|, den) = 1). All arithmetic and comparisons
 * are exact — no floating point is ever involved.
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) {
      throw new Error("Fraction: denominator must not be zero");
    }
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(absBigInt(num), den);
    this.num = num / g;
    this.den = den / g;
  }

  static zero(): Fraction {
    return new Fraction(0n, 1n);
  }

  static from(value: bigint | number | Fraction): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === "number") {
      if (!Number.isInteger(value)) {
        throw new Error(`Fraction: non-integer number cannot be exact: ${value}`);
      }
      return new Fraction(BigInt(value), 1n);
    }
    return new Fraction(value, 1n);
  }

  add(other: Fraction): Fraction {
    return new Fraction(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Fraction): Fraction {
    return new Fraction(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Fraction): Fraction {
    return new Fraction(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.num === 0n) {
      throw new Error("Fraction: division by zero");
    }
    return new Fraction(this.num * other.den, this.den * other.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  /** Exact comparison: returns -1, 0 or 1. */
  cmp(other: Fraction): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    if (lhs < rhs) return -1;
    if (lhs > rhs) return 1;
    return 0;
  }

  equals(other: Fraction): boolean {
    return this.num === other.num && this.den === other.den;
  }

  /** "p/q", or just "p" when the value is an integer. */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}

function absBigInt(v: bigint): bigint {
  return v < 0n ? -v : v;
}

function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}
