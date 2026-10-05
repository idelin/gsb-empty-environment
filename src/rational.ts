/**
 * Exact rational arithmetic over BigInt. All values are kept normalized:
 * denominator > 0 and gcd(|num|, den) = 1.
 */
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
    if (d === 0n) throw new Error("Rational: zero denominator");
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static zero(): Rational {
    return Rational.of(0n);
  }

  add(o: Rational): Rational {
    return Rational.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return Rational.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  mul(o: Rational): Rational {
    return Rational.of(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("Rational: division by zero");
    return Rational.of(this.num * o.den, this.den * o.num);
  }

  cmp(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  eq(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }
  lt(o: Rational): boolean {
    return this.cmp(o) < 0;
  }
  lte(o: Rational): boolean {
    return this.cmp(o) <= 0;
  }
  gt(o: Rational): boolean {
    return this.cmp(o) > 0;
  }
  gte(o: Rational): boolean {
    return this.cmp(o) >= 0;
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}

export function rmin(a: Rational, b: Rational): Rational {
  return a.lte(b) ? a : b;
}

export function rmax(a: Rational, b: Rational): Rational {
  return a.gte(b) ? a : b;
}
