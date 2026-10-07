/**
 * Exact rational arithmetic on BigInt. All values are kept in lowest terms
 * with a strictly positive denominator, so equality is `num === den` compare.
 */

export type RationalInput = Rational | bigint | number | string;

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
    if (den === 0n) throw new Error("Rational: zero denominator");
    if (num === 0n) {
      this.num = 0n;
      this.den = 1n;
      return;
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

  static of(num: bigint, den: bigint = 1n): Rational {
    return new Rational(num, den);
  }

  static zero(): Rational {
    return new Rational(0n, 1n);
  }

  static one(): Rational {
    return new Rational(1n, 1n);
  }

  /** Parses "3/2", "-0.25", "7", bigint, number (via its exact decimal string), or Rational. */
  static from(value: RationalInput): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === "bigint") return new Rational(value, 1n);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`Rational: non-finite number ${value}`);
      return Rational.from(value.toString());
    }
    const s = value.trim();
    const frac = /^([+-]?\d+)\s*\/\s*(\d+)$/.exec(s);
    if (frac) return new Rational(BigInt(frac[1]), BigInt(frac[2]));
    const dec = /^([+-]?)(\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (dec && (dec[2] !== "" || dec[3] !== undefined)) {
      const sign = dec[1] === "-" ? -1n : 1n;
      const intPart = dec[2] === "" ? 0n : BigInt(dec[2]);
      const fracDigits = dec[3] ?? "";
      const exp = dec[4] ? BigInt(dec[4]) : 0n;
      const scale = BigInt(fracDigits.length);
      let n = intPart * 10n ** scale + (fracDigits === "" ? 0n : BigInt(fracDigits));
      let d = 10n ** scale;
      if (exp >= 0n) n *= 10n ** exp;
      else d *= 10n ** (-exp);
      return new Rational(sign * n, d);
    }
    throw new Error(`Rational: cannot parse "${value}"`);
  }

  add(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.den + r.num * this.den, this.den * r.den);
  }

  sub(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.den - r.num * this.den, this.den * r.den);
  }

  mul(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.num, this.den * r.den);
  }

  div(o: RationalInput): Rational {
    const r = Rational.from(o);
    if (r.num === 0n) throw new Error("Rational: division by zero");
    return new Rational(this.num * r.den, this.den * r.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
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

  cmp(o: RationalInput): number {
    const r = Rational.from(o);
    const d = this.num * r.den - r.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }

  equals(o: RationalInput): boolean {
    const r = Rational.from(o);
    return this.num === r.num && this.den === r.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }
}

/** Convenience constructor. */
export function r(value: RationalInput): Rational {
  return Rational.from(value);
}
