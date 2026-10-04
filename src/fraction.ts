/**
 * Exact rational number backed by BigInt numerator/denominator.
 * Always kept normalized: gcd(|num|, den) == 1 and den > 0.
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(value: bigint | number | string | Fraction): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === "bigint") return new Fraction(value, 1n);
    if (typeof value === "number") {
      if (!Number.isInteger(value)) {
        throw new TypeError(
          `Fraction.of only accepts integers, got ${value}. ` +
            `Use Fraction.fromRatio for non-integer rationals.`
        );
      }
      return new Fraction(BigInt(value), 1n);
    }
    return Fraction.parse(value);
  }

  static fromRatio(num: bigint, den: bigint): Fraction {
    if (den === 0n) {
      throw new RangeError("Fraction.fromRatio: denominator is zero");
    }
    if (num === 0n) return new Fraction(0n, 1n);
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(abs(n), d);
    return new Fraction(n / g, d / g);
  }

  static parse(text: string): Fraction {
    const m = /^\s*([+-]?\d+)\s*(?:\/\s*([+-]?\d+)\s*)?$/.exec(text);
    if (!m) throw new TypeError(`Fraction.parse: invalid input ${JSON.stringify(text)}`);
    const num = BigInt(m[1]);
    const den = m[2] === undefined ? 1n : BigInt(m[2]);
    return Fraction.fromRatio(num, den);
  }

  get isZero(): boolean {
    return this.num === 0n;
  }

  get sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  add(other: Fraction): Fraction {
    return Fraction.fromRatio(
      this.num * other.den + other.num * this.den,
      this.den * other.den
    );
  }

  sub(other: Fraction): Fraction {
    return Fraction.fromRatio(
      this.num * other.den - other.num * this.den,
      this.den * other.den
    );
  }

  mul(other: Fraction): Fraction {
    return Fraction.fromRatio(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.num === 0n) {
      throw new RangeError("Fraction.div: division by zero");
    }
    return Fraction.fromRatio(this.num * other.den, this.den * other.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  /** Exact comparison: returns -1, 0 or 1. */
  cmp(other: Fraction): -1 | 0 | 1 {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  equals(other: Fraction): boolean {
    return this.num === other.num && this.den === other.den;
  }

  /** Lossy double approximation, for display only. */
  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}

export function abs(value: bigint): bigint {
  return value < 0n ? -value : value;
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = a;
  let y = b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}
