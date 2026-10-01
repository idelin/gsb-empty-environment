/**
 * Exact rational numbers on BigInt. No Number intermediates anywhere:
 * numerators/denominators are BigInt end to end.
 *
 * Normalization rules (applied in the constructor):
 *  - denominator is always > 0 (sign lives on the numerator);
 *  - the fraction is fully reduced by gcd;
 *  - NEGATIVE ZERO RULE: zero is canonical — 0/1 with positive sign.
 *    "-0", "-0.0", "-0/5" all normalize to the same zero and never
 *    render with a minus sign.
 */

export function gcd(a: bigint, b: bigint): bigint {
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

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new RangeError("denominator must not be zero");
    if (num === 0n) {
      // negative-zero normalization
      this.num = 0n;
      this.den = 1n;
      return;
    }
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static zero(): Rational {
    return new Rational(0n, 1n);
  }

  get isZero(): boolean {
    return this.num === 0n;
  }

  get isNegative(): boolean {
    return this.num < 0n;
  }

  /** Integer part truncated toward zero; remainder shares the sign of num. */
  split(): { int: bigint; rem: bigint; den: bigint } {
    const int = this.num / this.den;
    const rem = this.num - int * this.den;
    return { int, rem, den: this.den };
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}
