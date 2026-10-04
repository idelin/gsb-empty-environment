/**
 * Exact rational numbers (BigInt numerator/denominator, normalized).
 * Used by the independent certificate checker and by exact sample evaluation.
 */

import { Dec, pow10, type RoundingMode } from './decimal.ts';

function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export class Rat {
  readonly n: bigint;
  readonly d: bigint; // always > 0

  private constructor(n: bigint, d: bigint) {
    this.n = n;
    this.d = d;
  }

  static readonly zero: Rat = Rat.of(0n);
  static readonly one: Rat = Rat.of(1n);

  static of(n: bigint, d = 1n): Rat {
    if (d === 0n) throw new RangeError('Rat: zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    if (n === 0n) return new Rat(0n, 1n);
    const g = gcd(n < 0n ? -n : n, d);
    return new Rat(n / g, d / g);
  }

  static fromDec(x: Dec): Rat {
    return x.e >= 0 ? Rat.of(x.m * pow10(x.e)) : Rat.of(x.m, pow10(-x.e));
  }

  sign(): number {
    return this.n > 0n ? 1 : this.n < 0n ? -1 : 0;
  }

  static add(a: Rat, b: Rat): Rat {
    return Rat.of(a.n * b.d + b.n * a.d, a.d * b.d);
  }

  static sub(a: Rat, b: Rat): Rat {
    return Rat.of(a.n * b.d - b.n * a.d, a.d * b.d);
  }

  static mul(a: Rat, b: Rat): Rat {
    return Rat.of(a.n * b.n, a.d * b.d);
  }

  static div(a: Rat, b: Rat): Rat {
    if (b.n === 0n) throw new RangeError('Rat: division by zero');
    return Rat.of(a.n * b.d, a.d * b.n);
  }

  static neg(a: Rat): Rat {
    return Rat.of(-a.n, a.d);
  }

  static abs(a: Rat): Rat {
    return a.n < 0n ? Rat.of(-a.n, a.d) : a;
  }

  static cmp(a: Rat, b: Rat): number {
    const l = a.n * b.d;
    const r = b.n * a.d;
    return l < r ? -1 : l > r ? 1 : 0;
  }

  static min(a: Rat, b: Rat): Rat {
    return Rat.cmp(a, b) <= 0 ? a : b;
  }

  static max(a: Rat, b: Rat): Rat {
    return Rat.cmp(a, b) >= 0 ? a : b;
  }

  /** Convert to a decimal with explicit precision and rounding direction. */
  toDec(prec: number, mode: RoundingMode): Dec {
    return Dec.div(Dec.of(this.n), Dec.of(this.d), prec, mode);
  }

  toString(): string {
    return this.d === 1n ? this.n.toString() : `${this.n}/${this.d}`;
  }
}
