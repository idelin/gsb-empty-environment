/**
 * Exact rational number backed by BigInt, always kept in normalized form:
 * denominator > 0 and gcd(|num|, den) = 1.
 *
 * Every finite IEEE-754 double is a dyadic rational, so `Rat.fromNumber`
 * converts inputs losslessly. All arithmetic below is exact: no rounding
 * ever occurs, which is what makes the rasterizer's boundary decisions
 * (pixel center exactly on an edge / vertex) well-defined.
 */

function absBig(a: bigint): bigint {
  return a < 0n ? -a : a;
}

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
  /** Numerator (any sign). */
  readonly num: bigint;
  /** Denominator, always > 0. */
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  /** Build a normalized rational from numerator/denominator. */
  static of(num: bigint, den: bigint = 1n): Rat {
    if (den === 0n) throw new Error('Rat: zero denominator');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    if (num === 0n) return new Rat(0n, 1n);
    const g = gcd(num, den);
    return new Rat(num / g, den / g);
  }

  /** Lossless conversion of a finite double to its exact rational value. */
  static fromNumber(x: number): Rat {
    if (!Number.isFinite(x)) {
      throw new Error(`Rat.fromNumber: non-finite input ${x}`);
    }
    if (x === 0) return Rat.ZERO;
    const view = new DataView(new ArrayBuffer(8));
    view.setFloat64(0, x);
    const bits = view.getBigUint64(0);
    const negative = (bits >> 63n) === 1n;
    const expBits = Number((bits >> 52n) & 0x7ffn);
    const frac = bits & 0xfffffffffffffn;
    let mantissa: bigint;
    let exp: number;
    if (expBits === 0) {
      // Subnormal: value = frac * 2^-1074
      mantissa = frac;
      exp = -1074;
    } else {
      // Normal: value = (2^52 + frac) * 2^(expBits - 1075)
      mantissa = frac | (1n << 52n);
      exp = expBits - 1075;
    }
    let num = negative ? -mantissa : mantissa;
    let den = 1n;
    if (exp >= 0) {
      num <<= BigInt(exp);
    } else {
      den <<= BigInt(-exp);
    }
    return Rat.of(num, den);
  }

  static readonly ZERO: Rat = new Rat(0n, 1n);
  static readonly ONE: Rat = new Rat(1n, 1n);
  static readonly HALF: Rat = new Rat(1n, 2n);

  add(o: Rat): Rat {
    return Rat.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rat): Rat {
    return Rat.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rat): Rat {
    return Rat.of(this.num * o.num, this.den * o.den);
  }

  div(o: Rat): Rat {
    if (o.num === 0n) throw new Error('Rat: division by zero');
    return Rat.of(this.num * o.den, this.den * o.num);
  }

  neg(): Rat {
    return Rat.of(-this.num, this.den);
  }

  /** -1, 0 or 1. */
  cmp(o: Rat): number {
    const d = this.num * o.den - o.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }

  eq(o: Rat): boolean {
    return this.num === o.num && this.den === o.den;
  }

  /** True iff the value is an integer. */
  isInteger(): boolean {
    return this.den === 1n;
  }

  sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  /** Lossy conversion to double, for diagnostics only. */
  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}
