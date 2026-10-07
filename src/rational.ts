/**
 * Exact rational arithmetic on BigInt numerator/denominator.
 * All values are kept normalized: den > 0 and gcd(|num|, den) = 1.
 */

export type RationalLike = Rational | number | string | bigint;

function gcd(a: bigint, b: bigint): bigint {
  if (a < 0n) a = -a;
  if (b < 0n) b = -b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export class Rational {
  public readonly num: bigint;
  public readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error('Rational: zero denominator');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    if (num === 0n) return Rational.ZERO;
    const g = gcd(num, den);
    return new Rational(num / g, den / g);
  }

  /** Parse an exact rational from a number (finite decimal), bigint, or string
   *  like "3/4", "-2", "1.25", "1e-3". */
  static from(value: RationalLike): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return Rational.of(value);
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) {
        throw new Error(`Rational: non-finite number ${value}`);
      }
      return Rational.from(String(value));
    }
    const s = value.trim();
    const frac = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(s);
    if (frac) return Rational.of(BigInt(frac[1]), BigInt(frac[2]));
    const dec = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (dec) {
      const sign = dec[1] === '-' ? -1n : 1n;
      const intPart = dec[2];
      const fracPart = dec[3] ?? '';
      const exp = dec[4] ? BigInt(dec[4]) : 0n;
      const digits = BigInt(intPart + fracPart) * sign;
      const scale = BigInt(fracPart.length) - exp;
      if (scale >= 0n) return Rational.of(digits, 10n ** scale);
      return Rational.of(digits * 10n ** -scale, 1n);
    }
    throw new Error(`Rational: cannot parse "${value}"`);
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
    if (o.num === 0n) throw new Error('Rational: division by zero');
    return Rational.of(this.num * o.den, this.den * o.num);
  }
  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }
  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }
  isZero(): boolean {
    return this.num === 0n;
  }
  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }
  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }
  compare(o: Rational): -1 | 0 | 1 {
    const d = this.num * o.den - o.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }
  static max(a: Rational, b: Rational): Rational {
    return a.compare(b) >= 0 ? a : b;
  }
  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }
  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}
