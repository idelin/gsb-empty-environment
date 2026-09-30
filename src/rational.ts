/**
 * Exact rational arithmetic on BigInt.
 *
 * Rules:
 * - No Number is ever used as an intermediate for values; only BigInt.
 * - `fromInt` accepts a number only when it is a safe integer.
 * - Decimal text is parsed exactly (no binary floating point involved).
 * - Rounding happens ONLY in `toDecimal`, with an explicit RoundingMode.
 */

export type RoundingMode = 'floor' | 'ceil' | 'trunc' | 'half-up' | 'half-even';

const bigAbs = (x: bigint): bigint => (x < 0n ? -x : x);

const bigGcd = (a: bigint, b: bigint): bigint => {
  a = bigAbs(a);
  b = bigAbs(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
};

export class Rational {
  readonly num: bigint;
  readonly den: bigint; // always > 0, always reduced, zero is 0/1

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error('Rational: denominator must be non-zero');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    if (num === 0n) return new Rational(0n, 1n);
    const g = bigGcd(num, den);
    return new Rational(num / g, den / g);
  }

  static zero(): Rational {
    return Rational.of(0n);
  }

  static one(): Rational {
    return Rational.of(1n);
  }

  /** Accepts only safe integers; anything else must go through fromDecimal/of. */
  static fromInt(n: number): Rational {
    if (!Number.isSafeInteger(n)) {
      throw new Error(
        `Rational.fromInt: ${n} is not a safe integer; use fromDecimal(string) or of(bigint) instead`,
      );
    }
    return Rational.of(BigInt(n));
  }

  /** Exact decimal/scientific parser: "12", "-3.25", "1.5e-3", "9007199254740993.125". */
  static fromDecimal(text: string): Rational {
    const m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m) throw new Error(`Rational.fromDecimal: invalid decimal literal: ${JSON.stringify(text)}`);
    const [, sign, intPart, fracPart = '', expPart] = m;
    const digits = intPart + fracPart;
    let num = BigInt(digits);
    if (sign === '-') num = -num;
    let den = 10n ** BigInt(fracPart.length);
    if (expPart !== undefined) {
      const e = BigInt(expPart);
      if (e >= 0n) num *= 10n ** e;
      else den *= 10n ** -e;
    }
    return Rational.of(num, den);
  }

  static from(value: Rational | bigint | string | number): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return Rational.of(value);
    if (typeof value === 'string') return Rational.fromDecimal(value);
    return Rational.fromInt(value);
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
    if (o.num === 0n) throw new Error('Rational.div: division by zero');
    return Rational.of(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  /** -1, 0, 1 */
  sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(o: Rational): number {
    const d = this.num * o.den - o.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }

  eq(o: Rational): boolean {
    return this.cmp(o) === 0;
  }
  lt(o: Rational): boolean {
    return this.cmp(o) < 0;
  }
  le(o: Rational): boolean {
    return this.cmp(o) <= 0;
  }
  gt(o: Rational): boolean {
    return this.cmp(o) > 0;
  }
  ge(o: Rational): boolean {
    return this.cmp(o) >= 0;
  }

  static min(a: Rational, b: Rational): Rational {
    return a.le(b) ? a : b;
  }
  static max(a: Rational, b: Rational): Rational {
    return a.ge(b) ? a : b;
  }

  /** Exact "num/den" (or plain integer) form. Never rounds. */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * Decimal rendering with an EXPLICIT rounding rule. This is the only place
   * where information can be lost, and the caller chooses how.
   */
  toDecimal(places: number, mode: RoundingMode = 'trunc'): string {
    if (!Number.isSafeInteger(places) || places < 0) {
      throw new Error('Rational.toDecimal: places must be a non-negative safe integer');
    }
    const scale = 10n ** BigInt(places);
    const scaled = this.num * scale;
    let q = scaled / this.den; // BigInt division truncates toward zero
    const r = scaled % this.den;
    if (r !== 0n) {
      const absR = bigAbs(r);
      const positive = scaled > 0n;
      switch (mode) {
        case 'floor':
          if (!positive) q -= 1n;
          break;
        case 'ceil':
          if (positive) q += 1n;
          break;
        case 'trunc':
          break;
        case 'half-up': // half away from zero
          if (2n * absR >= this.den) q += positive ? 1n : -1n;
          break;
        case 'half-even': {
          const twice = 2n * absR;
          if (twice > this.den || (twice === this.den && bigAbs(q) % 2n === 1n)) {
            q += positive ? 1n : -1n;
          }
          break;
        }
      }
    }
    const neg = q < 0n;
    const digits = bigAbs(q).toString().padStart(places + 1, '0');
    if (places === 0) return (neg ? '-' : '') + digits;
    const intPart = digits.slice(0, digits.length - places);
    const fracPart = digits.slice(digits.length - places);
    return `${neg ? '-' : ''}${intPart}.${fracPart}`;
  }
}
