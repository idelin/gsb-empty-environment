/**
 * Exact rational numbers on BigInt. Always kept in lowest terms with den > 0.
 */
function absBig(a: bigint): bigint {
  return a < 0n ? -a : a;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = absBig(a);
  let y = absBig(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export class Frac {
  private constructor(readonly num: bigint, readonly den: bigint) {}

  static of(num: bigint, den: bigint = 1n): Frac {
    if (den === 0n) throw new Error("Frac: zero denominator");
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Frac(n / g, d / g);
  }

  /** Accepts "a", "-a", "a/b", "-a/b" (b a positive integer literal). */
  static parse(s: string): Frac {
    const t = s.trim();
    const m = /^([+-]?\d+)(?:\/([+-]?\d+))?$/.exec(t);
    if (!m) throw new Error(`invalid fraction: ${JSON.stringify(s)}`);
    const num = BigInt(m[1]);
    const den = m[2] !== undefined ? BigInt(m[2]) : 1n;
    if (den === 0n) throw new Error(`zero denominator in ${JSON.stringify(s)}`);
    return Frac.of(num, den);
  }

  static from(value: string | number | bigint | Frac): Frac {
    if (value instanceof Frac) return value;
    if (typeof value === "bigint") return Frac.of(value);
    if (typeof value === "number") {
      if (!Number.isSafeInteger(value))
        throw new Error(`not a safe integer: ${value}`);
      return Frac.of(BigInt(value));
    }
    return Frac.parse(value);
  }

  add(o: Frac): Frac {
    return Frac.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Frac): Frac {
    return Frac.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  mul(o: Frac): Frac {
    return Frac.of(this.num * o.num, this.den * o.den);
  }
  div(o: Frac): Frac {
    if (o.num === 0n) throw new Error("Frac: division by zero");
    return Frac.of(this.num * o.den, this.den * o.num);
  }
  neg(): Frac {
    return Frac.of(-this.num, this.den);
  }
  abs(): Frac {
    return this.num < 0n ? this.neg() : this;
  }
  sign(): number {
    return this.num === 0n ? 0 : this.num > 0n ? 1 : -1;
  }
  isZero(): boolean {
    return this.num === 0n;
  }
  cmp(o: Frac): number {
    const d = this.num * o.den - o.num * this.den;
    return d === 0n ? 0 : d > 0n ? 1 : -1;
  }
  eq(o: Frac): boolean {
    return this.num === o.num && this.den === o.den;
  }
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
  toJSON(): string {
    return this.toString();
  }
}

export const ZERO = Frac.of(0n);
export const ONE = Frac.of(1n);

export function dot(u: readonly Frac[], v: readonly Frac[]): Frac {
  let s = ZERO;
  for (let i = 0; i < u.length; i++) s = s.add(u[i].mul(v[i]));
  return s;
}
