/**
 * Exact rational arithmetic on BigInt. All operations are exact; no floats.
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint; // always > 0, always reduced

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new Error("Fraction: zero denominator");
    if (den < 0n) { num = -num; den = -den; }
    const g = gcd(absBig(num), den);
    this.num = num / g;
    this.den = den / g;
  }

  static readonly ZERO = new Fraction(0n);
  static readonly ONE = new Fraction(1n);

  static from(value: Fraction | bigint | number | string): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === "bigint") return new Fraction(value);
    if (typeof value === "number") {
      if (!Number.isInteger(value))
        throw new Error(`Fraction.from: non-integer number ${value}; pass "p/q" string instead`);
      return new Fraction(BigInt(value));
    }
    const s = value.trim();
    const slash = s.indexOf("/");
    if (slash >= 0) {
      return new Fraction(BigInt(s.slice(0, slash)), BigInt(s.slice(slash + 1)));
    }
    return new Fraction(BigInt(s));
  }

  get isZero(): boolean { return this.num === 0n; }
  sign(): number { return this.num > 0n ? 1 : this.num < 0n ? -1 : 0; }

  add(o: Fraction): Fraction {
    return new Fraction(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Fraction): Fraction {
    return new Fraction(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  mul(o: Fraction): Fraction {
    return new Fraction(this.num * o.num, this.den * o.den);
  }
  div(o: Fraction): Fraction {
    if (o.isZero) throw new Error("Fraction: division by zero");
    return new Fraction(this.num * o.den, this.den * o.num);
  }
  neg(): Fraction { return new Fraction(-this.num, this.den); }
  abs(): Fraction { return this.num < 0n ? this.neg() : this; }

  cmp(o: Fraction): number {
    const d = this.num * o.den - o.num * this.den;
    return d > 0n ? 1 : d < 0n ? -1 : 0;
  }
  eq(o: Fraction): boolean { return this.num === o.num && this.den === o.den; }

  toNumber(): number { return Number(this.num) / Number(this.den); }
  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}

export function absBig(x: bigint): bigint { return x < 0n ? -x : x; }

export function gcd(a: bigint, b: bigint): bigint {
  a = absBig(a); b = absBig(b);
  while (b !== 0n) { const t = a % b; a = b; b = t; }
  return a === 0n ? 1n : a;
}

export function lcm(a: bigint, b: bigint): bigint {
  return (a / gcd(a, b)) * b;
}
