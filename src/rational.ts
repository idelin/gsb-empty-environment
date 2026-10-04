/**
 * Exact rational arithmetic on BigInt, always kept in lowest terms with den > 0.
 * Used for map costs so that zero-cost cycles and fractional weights never
 * accumulate floating-point error.
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rational = Rational.raw(0n, 1n);
  static readonly ONE: Rational = Rational.raw(1n, 1n);

  private static raw(num: bigint, den: bigint): Rational {
    return new Rational(num, den);
  }

  private static gcd(a: bigint, b: bigint): bigint {
    a = a < 0n ? -a : a;
    b = b < 0n ? -b : b;
    while (b !== 0n) {
      const t = a % b;
      a = b;
      b = t;
    }
    return a === 0n ? 1n : a;
  }

  static of(value: bigint | number | string | Rational): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === "bigint") return Rational.raw(value, 1n);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`non-finite number: ${value}`);
      if (Number.isInteger(value)) return Rational.raw(BigInt(value), 1n);
      return Rational.parse(String(value));
    }
    return Rational.parse(value);
  }

  static frac(num: bigint, den: bigint): Rational {
    if (den === 0n) throw new Error("rational with zero denominator");
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = Rational.gcd(num, den);
    return Rational.raw(num / g, den / g);
  }

  /** Parses "a/b", decimals like "1.25", and integers like "-3". */
  static parse(text: string): Rational {
    const s = text.trim();
    const slash = s.indexOf("/");
    if (slash >= 0) {
      const a = s.slice(0, slash).trim();
      const b = s.slice(slash + 1).trim();
      if (!/^[+-]?\d+$/.test(a) || !/^[+-]?\d+$/.test(b)) {
        throw new Error(`bad rational: ${text}`);
      }
      return Rational.frac(BigInt(a), BigInt(b));
    }
    const m = /^([+-]?)(\d*)(?:\.(\d+))?$/.exec(s);
    if (!m || (m[2] === "" && !m[3])) throw new Error(`bad rational: ${text}`);
    const intPart = m[2] === "" ? "0" : m[2];
    const fracPart = m[3] ?? "";
    const scale = 10n ** BigInt(fracPart.length);
    const digits = BigInt(intPart + fracPart);
    const signed = m[1] === "-" ? -digits : digits;
    return Rational.frac(signed, scale);
  }

  add(other: Rational): Rational {
    return Rational.frac(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rational): Rational {
    return Rational.frac(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rational): Rational {
    return Rational.frac(this.num * other.num, this.den * other.den);
  }

  neg(): Rational {
    return Rational.raw(-this.num, this.den);
  }

  cmp(other: Rational): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  lt(o: Rational): boolean { return this.cmp(o) < 0; }
  le(o: Rational): boolean { return this.cmp(o) <= 0; }
  gt(o: Rational): boolean { return this.cmp(o) > 0; }
  ge(o: Rational): boolean { return this.cmp(o) >= 0; }
  eq(o: Rational): boolean { return this.cmp(o) === 0; }

  isZero(): boolean { return this.num === 0n; }
  isNegative(): boolean { return this.num < 0n; }

  static min(a: Rational, b: Rational): Rational { return a.le(b) ? a : b; }
  static max(a: Rational, b: Rational): Rational { return a.ge(b) ? a : b; }

  toNumber(): number { return Number(this.num) / Number(this.den); }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}
