/**
 * Exact rational numbers backed by bigint, always kept in normalized form:
 * denominator > 0 and gcd(|num|, den) = 1.
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  static of(value: bigint | number): Rational {
    if (typeof value === 'number') {
      if (!Number.isInteger(value)) {
        throw new Error(`Rational.of expects an integer, got ${value}`);
      }
      return new Rational(BigInt(value), 1n);
    }
    return new Rational(value, 1n);
  }

  /** Builds num/den, normalizing sign and reducing by gcd. */
  static fraction(num: bigint | number, den: bigint | number): Rational {
    let n = typeof num === 'number' ? BigInt(num) : num;
    let d = typeof den === 'number' ? BigInt(den) : den;
    if (d === 0n) throw new Error('Rational with zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    if (n === 0n) return Rational.ZERO;
    const g = gcd(abs(n), d);
    return new Rational(n / g, d / g);
  }

  /** Canonical string key, usable as a map key for deduplication. */
  get key(): string {
    return `${this.num}/${this.den}`;
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}

function abs(v: bigint): bigint {
  return v < 0n ? -v : v;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a;
  let y = b;
  while (y !== 0n) {
    const r = x % y;
    x = y;
    y = r;
  }
  return x === 0n ? 1n : x;
}

/** A 2D point with exact rational coordinates. */
export interface RationalPoint {
  readonly x: Rational;
  readonly y: Rational;
}

export function pointKey(p: RationalPoint): string {
  return `${p.x.key},${p.y.key}`;
}

export function pointToNumberPair(p: RationalPoint): [number, number] {
  return [p.x.toNumber(), p.y.toNumber()];
}
