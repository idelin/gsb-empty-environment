/**
 * Exact rational numbers backed by BigInt, always kept in lowest terms
 * with a strictly positive denominator.
 */
function gcd(a: bigint, b: bigint): bigint {
  if (a < 0n) a = -a;
  if (b < 0n) b = -b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export class Rational {
  private constructor(
    readonly num: bigint,
    readonly den: bigint,
  ) {}

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  static of(num: bigint | number, den: bigint | number = 1): Rational {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new Error("Rational: zero denominator");
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    if (n === 0n) return Rational.ZERO;
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  add(o: Rational): Rational {
    return Rational.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return Rational.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rational): Rational {
    return Rational.of(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("Rational: division by zero");
    return Rational.of(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isOne(): boolean {
    return this.num === this.den;
  }

  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  compare(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}
