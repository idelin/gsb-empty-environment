/**
 * Exact rational number arithmetic backed by BigInt.
 *
 * Every Fraction is kept in canonical form:
 *   - denominator is always positive
 *   - numerator and denominator are coprime (reduced by gcd)
 *   - zero is always 0/1
 *
 * Because all operations are exact, the simplex method built on top of this
 * class performs its basis exchanges (pivots) with zero floating-point loss.
 */

export type FractionInput = Fraction | bigint | number | string;

function absBigInt(value: bigint): bigint {
  return value < 0n ? -value : value;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = absBigInt(a);
  let y = absBigInt(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) {
      throw new Error("Fraction: denominator must not be zero");
    }
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static readonly ZERO: Fraction = new Fraction(0n, 1n);
  static readonly ONE: Fraction = new Fraction(1n, 1n);

  /**
   * Parses integers ("-3", 7n, 5), fractions ("-3/4") and finite decimals
   * ("0.125", "-2.5") into an exact Fraction. Numbers must be integers so
   * that no binary floating-point rounding can sneak into the model.
   */
  static from(value: FractionInput): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === "bigint") return new Fraction(value, 1n);
    if (typeof value === "number") {
      if (!Number.isSafeInteger(value)) {
        throw new Error(
          `Fraction.from: number ${value} is not a safe integer; ` +
            `pass a string like "${value}" or "p/q" for exact parsing`,
        );
      }
      return new Fraction(BigInt(value), 1n);
    }
    const text = value.trim();
    const fractionMatch = /^([+-]?\d+)\s*\/\s*(\d+)$/.exec(text);
    if (fractionMatch) {
      return new Fraction(BigInt(fractionMatch[1]), BigInt(fractionMatch[2]));
    }
    const decimalMatch = /^([+-]?)(\d*)(?:\.(\d+))?$/.exec(text);
    if (decimalMatch && (decimalMatch[2] !== "" || decimalMatch[3])) {
      const whole = decimalMatch[2] === "" ? "0" : decimalMatch[2];
      const frac = decimalMatch[3] ?? "";
      const scale = 10n ** BigInt(frac.length);
      const numerator = BigInt(whole) * scale + BigInt(frac === "" ? "0" : frac);
      return new Fraction(decimalMatch[1] === "-" ? -numerator : numerator, scale);
    }
    throw new Error(`Fraction.from: cannot parse "${value}"`);
  }

  static array(values: readonly FractionInput[]): Fraction[] {
    return values.map((v) => Fraction.from(v));
  }

  add(other: FractionInput): Fraction {
    const o = Fraction.from(other);
    return new Fraction(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: FractionInput): Fraction {
    const o = Fraction.from(other);
    return new Fraction(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: FractionInput): Fraction {
    const o = Fraction.from(other);
    return new Fraction(this.num * o.num, this.den * o.den);
  }

  div(other: FractionInput): Fraction {
    const o = Fraction.from(other);
    if (o.num === 0n) throw new Error("Fraction.div: division by zero");
    return new Fraction(this.num * o.den, this.den * o.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  abs(): Fraction {
    return this.num < 0n ? this.neg() : this;
  }

  /** Returns -1, 0 or 1. */
  compare(other: FractionInput): number {
    const o = Fraction.from(other);
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  equals(other: FractionInput): boolean {
    return this.compare(other) === 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isPositive(): boolean {
    return this.num > 0n;
  }

  isNegative(): boolean {
    return this.num < 0n;
  }

  /** Exact decimal expansion when it terminates, otherwise "num/den". */
  toString(): string {
    if (this.den === 1n) return this.num.toString();
    return `${this.num}/${this.den}`;
  }

  /** Lossy conversion, intended only for display/debugging. */
  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }
}

export function frac(value: FractionInput): Fraction {
  return Fraction.from(value);
}
