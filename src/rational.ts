/**
 * Exact rational number backed by bigint, always stored normalized:
 * den > 0 and gcd(|num|, den) = 1.
 */
export class Rat {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1): Rat {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new RangeError('rational denominator must be non-zero');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n < 0n ? -n : n, d);
    return new Rat(n / g, d / g);
  }

  static zero(): Rat {
    return Rat.of(0);
  }

  static one(): Rat {
    return Rat.of(1);
  }

  add(other: Rat): Rat {
    return Rat.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rat): Rat {
    return Rat.of(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rat): Rat {
    return Rat.of(this.num * other.num, this.den * other.den);
  }

  div(other: Rat): Rat {
    if (other.num === 0n) throw new RangeError('division by zero rational');
    return Rat.of(this.num * other.den, this.den * other.num);
  }

  neg(): Rat {
    return Rat.of(-this.num, this.den);
  }

  abs(): Rat {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(other: Rat): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  eq(other: Rat): boolean {
    return this.cmp(other) === 0;
  }

  lt(other: Rat): boolean {
    return this.cmp(other) < 0;
  }

  le(other: Rat): boolean {
    return this.cmp(other) <= 0;
  }

  gt(other: Rat): boolean {
    return this.cmp(other) > 0;
  }

  ge(other: Rat): boolean {
    return this.cmp(other) >= 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}

export function minRat(a: Rat, b: Rat): Rat {
  return a.le(b) ? a : b;
}

export function maxRat(a: Rat, b: Rat): Rat {
  return a.ge(b) ? a : b;
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
