// Exact rational arithmetic on BigInt. All values are kept normalized:
// denominator > 0 and gcd(|num|, den) = 1.

export type FractionInput = Fraction | bigint | number | string;

function gcdAbs (a: bigint, b: bigint) {
  let x = a; if (x < 0n) x = -x;
  let y = b; if (y < 0n) y = -y;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor (num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of (value: FractionInput) {
    if (value instanceof Fraction) return value;
    if (typeof value === 'bigint') return Fraction.fromBigInt(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isInteger(value)) {
        throw new Error('non-integer number ' + value + '; pass an exact "p/q" string instead');
      }
      return Fraction.fromBigInt(BigInt(value), 1n);
    }
    if (typeof value === 'string') return Fraction.parse(value);
    throw new Error('cannot convert to Fraction: ' + String(value));
  }

  static parse (text: string) {
    const parts = text.trim().split('/');
    if (parts.length === 1) return Fraction.fromBigInt(BigInt(parts[0].trim()), 1n);
    if (parts.length === 2) {
      return Fraction.fromBigInt(BigInt(parts[0].trim()), BigInt(parts[1].trim()));
    }
    throw new Error('invalid fraction: ' + text);
  }

  static fromBigInt (num: bigint, den: bigint) {
    if (den === 0n) throw new Error('zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) { n = -n; d = -d; }
    const g = gcdAbs(n, d);
    return new Fraction(n / g, d / g);
  }

  static zero () {
    return new Fraction(0n, 1n);
  }

  add (other: Fraction) {
    return Fraction.fromBigInt(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub (other: Fraction) {
    return Fraction.fromBigInt(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul (other: Fraction) {
    return Fraction.fromBigInt(this.num * other.num, this.den * other.den);
  }

  div (other: Fraction) {
    if (other.num === 0n) throw new Error('division by zero fraction');
    return Fraction.fromBigInt(this.num * other.den, this.den * other.num);
  }

  neg () {
    return new Fraction(-this.num, this.den);
  }

  abs () {
    if (this.num < 0n) return new Fraction(-this.num, this.den);
    return this;
  }

  cmp (other: Fraction) {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    if (lhs < rhs) return -1;
    if (lhs > rhs) return 1;
    return 0;
  }

  equals (other: Fraction) {
    return this.num === other.num && this.den === other.den;
  }

  sign () {
    if (this.num < 0n) return -1;
    if (this.num > 0n) return 1;
    return 0;
  }

  isZero () {
    return this.num === 0n;
  }

  static min (a: Fraction, b: Fraction) {
    if (a.cmp(b) <= 0) return a;
    return b;
  }

  static max (a: Fraction, b: Fraction) {
    if (a.cmp(b) >= 0) return a;
    return b;
  }

  toString () {
    if (this.den === 1n) return this.num.toString();
    return this.num.toString() + '/' + this.den.toString();
  }
}

export function F (value: FractionInput) {
  return Fraction.of(value);
}

// floor(a / b) for a positive divisor b, returned as a bigint (used for grid cells)
export function floorDiv (a: Fraction, b: Fraction) {
  if (b.sign() <= 0) throw new Error('floorDiv requires a positive divisor');
  const num = a.num * b.den;
  const den = a.den * b.num;
  let q = num / den;
  if (num % den !== 0n && num < 0n) q -= 1n;
  return q;
}
