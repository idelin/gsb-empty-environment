/**
 * Exact rational arithmetic on BigInt. All values are kept normalized:
 * denominator > 0 and gcd(|num|, den) = 1.
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1n): Rational {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new RangeError('rational with zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n < 0n ? -n : n, d);
    return new Rational(n / g, d / g);
  }

  static zero(): Rational {
    return Rational.of(0n);
  }

  add(other: Rational): Rational {
    return Rational.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rational): Rational {
    return Rational.of(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rational): Rational {
    return Rational.of(this.num * other.num, this.den * other.den);
  }

  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }

  /** -1, 0 or 1 */
  cmp(other: Rational): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  lt(o: Rational): boolean { return this.cmp(o) < 0; }
  le(o: Rational): boolean { return this.cmp(o) <= 0; }
  gt(o: Rational): boolean { return this.cmp(o) > 0; }
  ge(o: Rational): boolean { return this.cmp(o) >= 0; }
  eq(o: Rational): boolean { return this.cmp(o) === 0; }

  isZero(): boolean { return this.num === 0n; }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}

function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

/** Accepted shapes for a rational literal in policy/input data. */
export type RationalLike = number | string | { num: number | bigint; den?: number | bigint } | Rational;

/**
 * Converts a rational literal exactly. Numbers must be integers or finite
 * decimals (converted via their decimal string, so 0.1 is exactly 1/10).
 * Strings accept "num" or "num/den".
 */
export function toRational(value: RationalLike): Rational {
  if (value instanceof Rational) return value;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new RangeError(`non-finite rational: ${value}`);
    return decimalStringToRational(value.toString());
  }
  if (typeof value === 'string') {
    const m = /^(-?\d+)(?:\/(\d+))?$/.exec(value.trim());
    if (!m) throw new RangeError(`invalid rational literal: ${value}`);
    return Rational.of(BigInt(m[1]), m[2] ? BigInt(m[2]) : 1n);
  }
  return Rational.of(value.num, value.den ?? 1);
}

function decimalStringToRational(s: string): Rational {
  const m = /^(-?)(\d+)(?:\.(\d+))?(?:e([+-]?\d+))?$/i.exec(s);
  if (!m) throw new RangeError(`cannot convert to rational exactly: ${s}`);
  const [, sign, intPart, fracPart = '', expPart] = m;
  let digits = BigInt(sign + intPart + fracPart);
  let scale = BigInt(10) ** BigInt(fracPart.length);
  if (expPart !== undefined) {
    const e = BigInt(expPart);
    if (e >= 0n) digits *= 10n ** e;
    else scale *= 10n ** -e;
  }
  return Rational.of(digits, scale);
}
