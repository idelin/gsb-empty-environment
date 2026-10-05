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

/**
 * 约分后的整数分数。所有算术都是精确的，不经过浮点。
 * 规范化不变量：den > 0，gcd(|num|, den) = 1。
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number | Fraction, den: bigint | number = 1): Fraction {
    if (num instanceof Fraction) {
      if (den === 1 || den === 1n) return num;
      return Fraction.of(num.num, num.den * BigInt(den));
    }
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new RangeError('fraction denominator must not be zero');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Fraction(n / g, d / g);
  }

  static zero(): Fraction {
    return Fraction.of(0);
  }

  static one(): Fraction {
    return Fraction.of(1);
  }

  add(other: Fraction): Fraction {
    return Fraction.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Fraction): Fraction {
    return this.add(other.neg());
  }

  mul(other: Fraction): Fraction {
    return Fraction.of(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.num === 0n) throw new RangeError('division by zero fraction');
    return Fraction.of(this.num * other.den, this.den * other.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isNegative(): boolean {
    return this.num < 0n;
  }

  equals(other: Fraction): boolean {
    return this.num === other.num && this.den === other.den;
  }

  compare(other: Fraction): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}

export function frac(num: bigint | number | Fraction, den: bigint | number = 1): Fraction {
  return Fraction.of(num, den);
}
