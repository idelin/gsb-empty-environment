/**
 * Exact rational arithmetic on BigInt numerator/denominator.
 * All values are stored normalized: den > 0 and gcd(|num|, den) = 1.
 */

export type FractionLike = Fraction | number | string | bigint;

function absBig(value: bigint): bigint {
  return value < 0n ? -value : value;
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

export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Fraction = new Fraction(0n, 1n);
  static readonly ONE: Fraction = new Fraction(1n, 1n);

  static of(value: FractionLike): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === 'bigint') return new Fraction(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) {
        throw new Error('not a finite number: ' + value);
      }
      return Fraction.parse(value.toString());
    }
    return Fraction.parse(value);
  }

  static fromBigInts(num: bigint, den: bigint): Fraction {
    if (den === 0n) throw new Error('rational with zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcdBig(n, d);
    return new Fraction(n / g, d / g);
  }

  /** Accepts "3/4", "5", "-2", "1.25", "1e-3", "-0.5". */
  static parse(text: string): Fraction {
    const s = text.trim();
    const slash = s.indexOf('/');
    if (slash >= 0) {
      const n = BigInt(s.slice(0, slash).trim());
      const d = BigInt(s.slice(slash + 1).trim());
      return Fraction.fromBigInts(n, d);
    }
    const m = /^([+-]?)(\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (m && (m[2] !== '' || m[3] !== undefined)) {
      const sign = m[1] === '-' ? -1n : 1n;
      const intPart = m[2] === '' ? 0n : BigInt(m[2]);
      const fracDigits = m[3] ?? '';
      const exponent = m[4] === undefined ? 0n : BigInt(m[4]);
      const den0 = 10n ** BigInt(fracDigits.length);
      let den = den0;
      let num = sign * (intPart * den0 + BigInt(fracDigits === '' ? '0' : fracDigits));
      if (exponent >= 0n) {
        num *= 10n ** exponent;
      } else {
        den *= 10n ** -exponent;
      }
      return Fraction.fromBigInts(num, den);
    }
    throw new Error('cannot parse rational: ' + JSON.stringify(text));
  }

  add(other: FractionLike): Fraction {
    const o = Fraction.of(other);
    return Fraction.fromBigInts(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: FractionLike): Fraction {
    const o = Fraction.of(other);
    return Fraction.fromBigInts(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: FractionLike): Fraction {
    const o = Fraction.of(other);
    return Fraction.fromBigInts(this.num * o.num, this.den * o.den);
  }

  div(other: FractionLike): Fraction {
    const o = Fraction.of(other);
    return Fraction.fromBigInts(this.num * o.den, this.den * o.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  /** Returns -1, 0 or 1. */
  compare(other: FractionLike): number {
    const o = Fraction.of(other);
    const diff = this.num * o.den - o.num * this.den;
    return diff < 0n ? -1 : diff > 0n ? 1 : 0;
  }

  equals(other: FractionLike): boolean {
    return this.compare(other) === 0;
  }

  lt(other: FractionLike): boolean {
    return this.compare(other) < 0;
  }

  lte(other: FractionLike): boolean {
    return this.compare(other) <= 0;
  }

  gt(other: FractionLike): boolean {
    return this.compare(other) > 0;
  }

  gte(other: FractionLike): boolean {
    return this.compare(other) >= 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isNegative(): boolean {
    return this.num < 0n;
  }

  get sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  static min(a: Fraction, b: Fraction): Fraction {
    return a.lte(b) ? a : b;
  }

  static max(a: Fraction, b: Fraction): Fraction {
    return a.gte(b) ? a : b;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : this.num.toString() + '/' + this.den.toString();
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toJSON(): string {
    return this.toString();
  }
}
