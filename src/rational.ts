/**
 * Exact rational arithmetic on BigInt. Every Rat is kept normalized:
 * den > 0 and gcd(|num|, den) = 1.
 */
export type RatInput = Rat | bigint | number | string;

const absBig = (x: bigint): bigint => (x < 0n ? -x : x);

function gcd(a: bigint, b: bigint): bigint {
  a = absBig(a);
  b = absBig(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export class Rat {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static of(num: bigint, den: bigint = 1n): Rat {
    if (den === 0n) throw new Error("Rat: zero denominator");
    return new Rat(num, den);
  }

  static from(value: RatInput): Rat {
    if (value instanceof Rat) return value;
    if (typeof value === "bigint") return Rat.of(value);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`Rat: non-finite number ${value}`);
      return Rat.parse(String(value));
    }
    return Rat.parse(value);
  }

  /** Parses "a/b", integers, decimals and scientific notation exactly. */
  static parse(text: string): Rat {
    const s = text.trim();
    const frac = s.match(/^([+-]?\d+)\s*\/\s*([+-]?\d+)$/);
    if (frac) return Rat.of(BigInt(frac[1]), BigInt(frac[2]));
    const m = s.match(/^([+-]?)(\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/);
    if (!m || (m[2] === "" && m[3] === undefined)) {
      throw new Error(`Rat: cannot parse "${text}"`);
    }
    const digits = (m[2] ?? "") + (m[3] ?? "");
    let num = BigInt((m[1] === "-" ? "-" : "") + (digits === "" ? "0" : digits));
    const exp = (m[4] !== undefined ? BigInt(m[4]) : 0n) - BigInt((m[3] ?? "").length);
    let den = 1n;
    if (exp >= 0n) num *= 10n ** exp;
    else den = 10n ** -exp;
    return Rat.of(num, den);
  }

  add(o: RatInput): Rat {
    const r = Rat.from(o);
    return Rat.of(this.num * r.den + r.num * this.den, this.den * r.den);
  }

  sub(o: RatInput): Rat {
    const r = Rat.from(o);
    return Rat.of(this.num * r.den - r.num * this.den, this.den * r.den);
  }

  mul(o: RatInput): Rat {
    const r = Rat.from(o);
    return Rat.of(this.num * r.num, this.den * r.den);
  }

  div(o: RatInput): Rat {
    const r = Rat.from(o);
    if (r.num === 0n) throw new Error("Rat: division by zero");
    return Rat.of(this.num * r.den, this.den * r.num);
  }

  neg(): Rat {
    return Rat.of(-this.num, this.den);
  }

  abs(): Rat {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(o: RatInput): number {
    const r = Rat.from(o);
    const lhs = this.num * r.den;
    const rhs = r.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  equals(o: RatInput): boolean {
    return this.cmp(o) === 0;
  }

  sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }

  static min(a: Rat, b: Rat): Rat {
    return a.cmp(b) <= 0 ? a : b;
  }

  static max(a: Rat, b: Rat): Rat {
    return a.cmp(b) >= 0 ? a : b;
  }
}
