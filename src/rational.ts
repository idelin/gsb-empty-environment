/**
 * Exact rational numbers backed by BigInt.
 * This is the only numeric type used by the comparator: no floats anywhere.
 */

const absBig = (x: bigint): bigint => (x < 0n ? -x : x);

export function gcdBig(a: bigint, b: bigint): bigint {
  a = absBig(a);
  b = absBig(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export function lcmBig(a: bigint, b: bigint): bigint {
  if (a === 0n || b === 0n) return 0n;
  return absBig((a / gcdBig(a, b)) * b);
}

/** floor(sqrt(n)) for n >= 0, exact (Newton iteration on BigInt). */
export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new Error('isqrt of a negative number');
  if (n < 2n) return n;
  const bitLen = BigInt(n.toString(2).length);
  let x = 1n << ((bitLen + 1n) >> 1n); // overestimate
  for (;;) {
    const y = (x + n / x) >> 1n;
    if (y >= x) return x;
    x = y;
  }
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint; // always > 0, gcd(|num|, den) = 1

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static make(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error('Rational: zero denominator');
    if (num === 0n) return new Rational(0n, 1n);
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcdBig(num, den);
    return new Rational(num / g, den / g);
  }

  static of(value: number | bigint | string | Rational): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return Rational.make(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isInteger(value)) throw new Error(`Rational.of: not an integer: ${value}`);
      return Rational.make(BigInt(value), 1n);
    }
    const m = /^(-?\d+)(?:\/(\d+))?$/.exec(value.trim());
    if (!m) throw new Error(`Rational.of: cannot parse "${value}"`);
    return Rational.make(BigInt(m[1]), m[2] ? BigInt(m[2]) : 1n);
  }

  static readonly ZERO: Rational = Rational.make(0n, 1n);
  static readonly ONE: Rational = Rational.make(1n, 1n);
  static readonly TWO: Rational = Rational.make(2n, 1n);

  add(o: Rational): Rational {
    return Rational.make(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Rational): Rational {
    return Rational.make(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  mul(o: Rational): Rational {
    return Rational.make(this.num * o.num, this.den * o.den);
  }
  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error('Rational: division by zero');
    return Rational.make(this.num * o.den, this.den * o.num);
  }
  neg(): Rational {
    return Rational.make(-this.num, this.den);
  }
  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }
  inv(): Rational {
    if (this.num === 0n) throw new Error('Rational: inverse of zero');
    return Rational.make(this.den, this.num);
  }
  half(): Rational {
    return Rational.make(this.num, this.den * 2n);
  }

  cmp(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }
  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }
  sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }
  isZero(): boolean {
    return this.num === 0n;
  }
  isOne(): boolean {
    return this.num === this.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** Exact truncated decimal expansion (BigInt only, no floats). */
  toDecimal(digits = 10): string {
    const scale = 10n ** BigInt(digits);
    const neg = this.num < 0n;
    const n = neg ? -this.num : this.num;
    const q = (n * scale) / this.den;
    const intPart = q / scale;
    const frac = (q % scale).toString().padStart(digits, '0');
    return (neg ? '-' : '') + intPart.toString() + (digits > 0 ? '.' + frac : '');
  }
}
