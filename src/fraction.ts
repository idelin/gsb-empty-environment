/**
 * Exact rational numbers backed by bigint.
 * Normalized: denominator > 0, gcd(|num|, den) = 1.
 */
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
    if (d === 0n) throw new Error("Fraction: zero denominator");
    if (d < 0n) { n = -n; d = -d; }
    if (n === 0n) return new Fraction(0n, 1n);
    const g = gcd(n < 0n ? -n : n, d);
    return new Fraction(n / g, d / g);
  }

  static zero(): Fraction { return Fraction.of(0); }
  static one(): Fraction { return Fraction.of(1); }

  get isZero(): boolean { return this.num === 0n; }
  get isOne(): boolean { return this.num === this.den; }

  add(o: Fraction): Fraction {
    return Fraction.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Fraction): Fraction {
    return Fraction.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  mul(o: Fraction): Fraction {
    return Fraction.of(this.num * o.num, this.den * o.den);
  }
  div(o: Fraction): Fraction {
    if (o.num === 0n) throw new Error("Fraction: division by zero");
    return Fraction.of(this.num * o.den, this.den * o.num);
  }
  neg(): Fraction { return Fraction.of(-this.num, this.den); }

  /** this < o */
  lt(o: Fraction): boolean { return this.num * o.den < o.num * this.den; }
  equals(o: Fraction): boolean { return this.num === o.num && this.den === o.den; }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
  toNumber(): number { return Number(this.num) / Number(this.den); }
}

function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) { const t = a % b; a = b; b = t; }
  return a === 0n ? 1n : a;
}

/** Convenience: Fraction.of(n, d). */
export function frac(num: bigint | number, den: bigint | number = 1n): Fraction {
  return Fraction.of(num, den);
}
