/**
 * Exact rational arithmetic on BigInt numerator/denominator.
 * Invariants: den > 0, gcd(|num|, den) = 1.
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO = new Rational(0n, 1n);
  static readonly ONE = new Rational(1n, 1n);

  static of(value: Rational | number | string | bigint): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === "bigint") return new Rational(value, 1n);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`not a finite number: ${value}`);
      return Rational.parse(String(value));
    }
    return Rational.parse(value);
  }

  /** Parses "p/q", integers, decimals and scientific notation exactly. */
  static parse(text: string): Rational {
    const s = text.trim();
    const frac = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(s);
    if (frac) return Rational.make(BigInt(frac[1]), BigInt(frac[2]));
    const dec = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (dec && (dec[2] !== "" || (dec[3] ?? "") !== "")) {
      const sign = dec[1] === "-" ? -1n : 1n;
      const digits = (dec[2] || "") + (dec[3] || "");
      if (digits === "") throw new Error(`invalid rational: ${text}`);
      const exp = BigInt(dec[4] ?? "0") - BigInt((dec[3] || "").length);
      const mantissa = sign * BigInt(digits === "" ? "0" : digits);
      return exp >= 0n
        ? Rational.make(mantissa * 10n ** exp, 1n)
        : Rational.make(mantissa, 10n ** -exp);
    }
    throw new Error(`invalid rational: ${text}`);
  }

  static make(num: bigint, den: bigint): Rational {
    if (den === 0n) throw new Error("division by zero");
    if (den < 0n) { num = -num; den = -den; }
    if (num === 0n) return Rational.ZERO;
    const g = gcd(num < 0n ? -num : num, den);
    return new Rational(num / g, den / g);
  }

  add(o: Rational): Rational {
    return Rational.make(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Rational): Rational {
    return Rational.make(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  mul(o: Rational): Rational {
    return Rational.make(this.num * o.num, this.den * o.den);
  }
  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("division by zero");
    return Rational.make(this.num * o.den, this.den * o.num);
  }
  neg(): Rational { return new Rational(-this.num, this.den); }
  abs(): Rational { return this.num < 0n ? this.neg() : this; }

  sign(): number { return this.num === 0n ? 0 : this.num > 0n ? 1 : -1; }
  cmp(o: Rational): number {
    const d = this.num * o.den - o.num * this.den;
    return d === 0n ? 0 : d > 0n ? 1 : -1;
  }
  eq(o: Rational): boolean { return this.num === o.num && this.den === o.den; }
  isZero(): boolean { return this.num === 0n; }

  static min(a: Rational, b: Rational): Rational { return a.cmp(b) <= 0 ? a : b; }
  static max(a: Rational, b: Rational): Rational { return a.cmp(b) >= 0 ? a : b; }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
  toJSON(): string { return this.toString(); }
}

export function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) { const t = a % b; a = b; b = t; }
  return a === 0n ? 1n : a;
}

export function lcm(a: bigint, b: bigint): bigint {
  return (a / gcd(a, b)) * b;
}
