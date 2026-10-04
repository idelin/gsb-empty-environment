/**
 * 精确有理数：BigInt 分子分母，规范化（分母为正、约分）。
 * 排序库的所有比较都建立在精确算术上，绝不经过浮点。
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1): Rational {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new Error('Rational: denominator must be non-zero');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(abs(n), d);
    return new Rational(n / g, d / g);
  }

  static zero(): Rational {
    return Rational.of(0);
  }

  cmp(other: Rational): -1 | 0 | 1 {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  eq(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  lt(other: Rational): boolean {
    return this.cmp(other) < 0;
  }

  gt(other: Rational): boolean {
    return this.cmp(other) > 0;
  }

  sub(other: Rational): Rational {
    return Rational.normalize(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  add(other: Rational): Rational {
    return Rational.normalize(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  private static normalize(num: bigint, den: bigint): Rational {
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(abs(num), den);
    return new Rational(num / g, den / g);
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}

function abs(x: bigint): bigint {
  return x < 0n ? -x : x;
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
