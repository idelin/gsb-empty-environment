const gcdBigInt = (x: bigint, y: bigint): bigint => {
  let a = x < 0n ? -x : x;
  let b = y < 0n ? -y : y;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
};

export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1n): Fraction {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new Error('Fraction with zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcdBigInt(n, d);
    return new Fraction(n / g, d / g);
  }

  static zero(): Fraction {
    return Fraction.of(0n);
  }

  add(other: Fraction): Fraction {
    return Fraction.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Fraction): Fraction {
    return Fraction.of(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Fraction): Fraction {
    return Fraction.of(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.num === 0n) throw new Error('Division by zero fraction');
    return Fraction.of(this.num * other.den, this.den * other.num);
  }

  neg(): Fraction {
    return Fraction.of(-this.num, this.den);
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(other: Fraction): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  equals(other: Fraction): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}
