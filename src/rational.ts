/**
 * Exact rational numbers (num/den, normalized, den > 0) used by the
 * independent certificate checker and the counterexample search.
 */

import { Decimal, pow10 } from './decimal.ts';

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
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

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);
  static readonly TWO: Rational = new Rational(2n, 1n);

  static of(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new RangeError('Rational.of: zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static fromDecimal(d: Decimal): Rational {
    if (d.exp >= 0) return Rational.of(d.coeff * pow10(d.exp));
    return Rational.of(d.coeff, pow10(-d.exp));
  }

  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? new Rational(-this.num, this.den) : this;
  }

  add(o: Rational): Rational {
    return Rational.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return this.add(o.neg());
  }

  mul(o: Rational): Rational {
    return Rational.of(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new RangeError('Rational.div: division by zero');
    return Rational.of(this.num * o.den, this.den * o.num);
  }

  cmp(o: Rational): number {
    const a = this.num * o.den;
    const b = o.num * this.den;
    return a < b ? -1 : a > b ? 1 : 0;
  }

  eq(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }
}
