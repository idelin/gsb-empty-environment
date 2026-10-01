/**
 * Exact rational arithmetic on BigInt.
 *
 * Design rules:
 * - No `Number` is ever used to carry a value; all magnitudes are BigInt.
 * - All arithmetic is exact. Rounding happens ONLY in `toDecimal`, and the
 *   caller must pass an explicit RoundingMode (rounding is never implicit).
 */
export type RoundingMode =
  | "floor"
  | "ceil"
  | "towardZero"
  | "awayFromZero"
  | "halfUp"
  | "halfEven";

export const ROUNDING_MODES: readonly RoundingMode[] = [
  "floor",
  "ceil",
  "towardZero",
  "awayFromZero",
  "halfUp",
  "halfEven",
];

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcdBig(a: bigint, b: bigint): bigint {
  let x = absBig(a);
  let y = absBig(b);
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
    this.num = num;
    this.den = den;
  }

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error("Rational: zero denominator");
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcdBig(n, d);
    return new Rational(n / g, d / g);
  }

  static fromBigInt(n: bigint): Rational {
    return new Rational(n, 1n);
  }

  static zero(): Rational {
    return new Rational(0n, 1n);
  }

  static one(): Rational {
    return new Rational(1n, 1n);
  }

  /**
   * Exact decimal/scientific parser, e.g. "12", "-3.25", "0.1", "1.5e-3".
   * Implemented on BigInt digit strings; never routes through Number.
   */
  static parse(text: string): Rational {
    const m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m) throw new Error(`Rational.parse: invalid decimal literal: ${text}`);
    const sign = m[1] === "-" ? -1n : 1n;
    const intPart = m[2];
    const fracPart = m[3] ?? "";
    const exp = m[4] === undefined ? 0n : BigInt(m[4]);
    const digits = BigInt(intPart + fracPart);
    let num = sign * digits;
    let den = 1n;
    const shift = BigInt(fracPart.length) - exp;
    if (shift >= 0n) {
      den = 10n ** shift;
    } else {
      num *= 10n ** -shift;
    }
    return Rational.of(num, den);
  }

  add(o: Rational): Rational {
    return Rational.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return Rational.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rational): Rational {
    return Rational.of(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("Rational: division by zero");
    return Rational.of(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  sign(): number {
    return this.num === 0n ? 0 : this.num > 0n ? 1 : -1;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  eq(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  lt(o: Rational): boolean {
    return this.cmp(o) < 0;
  }

  lte(o: Rational): boolean {
    return this.cmp(o) <= 0;
  }

  gt(o: Rational): boolean {
    return this.cmp(o) > 0;
  }

  gte(o: Rational): boolean {
    return this.cmp(o) >= 0;
  }

  static min(a: Rational, b: Rational): Rational {
    return a.lte(b) ? a : b;
  }

  static max(a: Rational, b: Rational): Rational {
    return a.gte(b) ? a : b;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * Decimal rendering with `scale` digits after the point.
   * The rounding mode is a REQUIRED argument: rounding is always explicit.
   */
  toDecimal(scale: number, mode: RoundingMode): string {
    if (!Number.isInteger(scale) || scale < 0) {
      throw new Error("toDecimal: scale must be a non-negative integer");
    }
    const factor = 10n ** BigInt(scale);
    const scaled = this.num * factor;
    const q = scaled / this.den;
    const r = scaled % this.den;
    let adjust = 0n;
    if (r !== 0n) {
      const positive = scaled > 0n;
      switch (mode) {
        case "floor":
          adjust = positive ? 0n : -1n;
          break;
        case "ceil":
          adjust = positive ? 1n : 0n;
          break;
        case "towardZero":
          adjust = 0n;
          break;
        case "awayFromZero":
          adjust = positive ? 1n : -1n;
          break;
        case "halfUp": {
          const twice = absBig(r) * 2n;
          if (twice >= this.den) adjust = positive ? 1n : -1n;
          break;
        }
        case "halfEven": {
          const twice = absBig(r) * 2n;
          if (twice > this.den) {
            adjust = positive ? 1n : -1n;
          } else if (twice === this.den && (q < 0n ? -q : q) % 2n === 1n) {
            adjust = positive ? 1n : -1n;
          }
          break;
        }
      }
    }
    const rounded = q + adjust;
    const negative = rounded < 0n;
    const digits = (negative ? -rounded : rounded).toString().padStart(scale + 1, "0");
    const intPart = scale === 0 ? digits : digits.slice(0, digits.length - scale);
    const fracPart = scale === 0 ? "" : "." + digits.slice(digits.length - scale);
    return (negative ? "-" : "") + intPart + fracPart;
  }
}

export function rat(text: string): Rational {
  return Rational.parse(text);
}

export function ratInt(n: bigint): Rational {
  return Rational.fromBigInt(n);
}
