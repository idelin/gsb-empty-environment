/**
 * Exact rational number arithmetic on bigints.
 * Every value is kept in lowest terms with a positive denominator,
 * so no precision is ever lost during simplex pivoting.
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1n): Rational {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new Error('Rational: zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n < 0n ? -n : n, d);
    return new Rational(n / g, d / g);
  }

  /** Parses "a/b", "-a/b" or an integer string. */
  static parse(text: string): Rational {
    const trimmed = text.trim();
    const slash = trimmed.indexOf('/');
    if (slash === -1) return Rational.of(BigInt(trimmed));
    const n = BigInt(trimmed.slice(0, slash));
    const d = BigInt(trimmed.slice(slash + 1));
    return Rational.of(n, d);
  }

  static zero(): Rational {
    return Rational.of(0n);
  }

  static one(): Rational {
    return Rational.of(1n);
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
    if (other.num === 0n) throw new Error('Rational: division by zero');
    return Rational.of(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  sign(): number {
    return this.num > 0n ? 1 : this.num < 0n ? -1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(other: Rational): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs > rhs ? 1 : lhs < rhs ? -1 : 0;
  }

  eq(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a;
  let y = b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}
