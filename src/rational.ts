/**
 * Exact rational arithmetic on BigInt.
 * Invariants: den > 0, gcd(|num|, den) = 1.
 */
export class Rat {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rat = new Rat(0n, 1n);
  static readonly ONE: Rat = new Rat(1n, 1n);

  static frac(num: bigint, den: bigint): Rat {
    if (den === 0n) throw new Error("Rat: division by zero");
    if (num === 0n) return Rat.ZERO;
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(absBig(n), d);
    return new Rat(n / g, d / g);
  }

  static of(value: Rat | bigint | number | string): Rat {
    if (value instanceof Rat) return value;
    if (typeof value === "bigint") return new Rat(value, 1n);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`Rat: non-finite number ${value}`);
      return Rat.parse(String(value));
    }
    return Rat.parse(value);
  }

  /** Parses "a/b", integer, or exact decimal (incl. exponent) strings. */
  static parse(text: string): Rat {
    const s = text.trim();
    const slash = s.indexOf("/");
    if (slash >= 0) {
      const n = BigInt(s.slice(0, slash).trim());
      const d = BigInt(s.slice(slash + 1).trim());
      return Rat.frac(n, d);
    }
    const m = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (!m || (m[2] === "" && (m[3] === undefined || m[3] === ""))) {
      throw new Error(`Rat: cannot parse "${text}"`);
    }
    const sign = m[1] === "-" ? -1n : 1n;
    const intPart = m[2] === "" ? "0" : m[2];
    const fracPart = m[3] ?? "";
    const exp = m[4] === undefined ? 0n : BigInt(m[4]);
    const digits = intPart + fracPart;
    const scale = BigInt(fracPart.length);
    let num = sign * BigInt(digits === "" ? "0" : digits);
    let den = 1n;
    const e = exp - scale;
    if (e >= 0n) num *= 10n ** e;
    else den = 10n ** -e;
    return Rat.frac(num, den);
  }

  get isZero(): boolean {
    return this.num === 0n;
  }

  sign(): number {
    return this.num === 0n ? 0 : this.num > 0n ? 1 : -1;
  }

  add(o: Rat): Rat {
    return Rat.frac(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rat): Rat {
    return Rat.frac(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rat): Rat {
    return Rat.frac(this.num * o.num, this.den * o.den);
  }

  div(o: Rat): Rat {
    if (o.num === 0n) throw new Error("Rat: division by zero");
    return Rat.frac(this.num * o.den, this.den * o.num);
  }

  neg(): Rat {
    return new Rat(-this.num, this.den);
  }

  abs(): Rat {
    return this.num < 0n ? new Rat(-this.num, this.den) : this;
  }

  cmp(o: Rat): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  eq(o: Rat): boolean {
    return this.num === o.num && this.den === o.den;
  }

  lt(o: Rat): boolean {
    return this.cmp(o) < 0;
  }

  lte(o: Rat): boolean {
    return this.cmp(o) <= 0;
  }

  gt(o: Rat): boolean {
    return this.cmp(o) > 0;
  }

  gte(o: Rat): boolean {
    return this.cmp(o) >= 0;
  }

  static min(a: Rat, b: Rat): Rat {
    return a.lte(b) ? a : b;
  }

  static max(a: Rat, b: Rat): Rat {
    return a.gte(b) ? a : b;
  }

  /** floor(this / divisor), exact, for arbitrary sign. */
  floorDiv(divisor: Rat): bigint {
    if (divisor.num === 0n) throw new Error("Rat.floorDiv: zero divisor");
    const n = this.num * divisor.den;
    const d = this.den * divisor.num;
    let q = n / d;
    const r = n % d;
    if (r !== 0n && (r > 0n) !== (d > 0n)) q -= 1n;
    return q;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
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

function absBig(v: bigint): bigint {
  return v < 0n ? -v : v;
}
