/**
 * Exact rational arithmetic used as the reference model for residual and
 * boundary-locking tests. The engine itself runs on floats; tests replay the
 * same event sequences through this model and require exact agreement for
 * dyadic inputs (where float arithmetic is exact) and bounded error otherwise.
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static of(num: bigint | number, den: bigint | number = 1): Fraction {
    let n = BigInt(num);
    let d = BigInt(den);
    if (d === 0n) throw new Error('Fraction with zero denominator');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(absBig(n), d);
    return new Fraction(n / g, d / g);
  }

  /** Parses a finite decimal string such as "0.1" or "-3.125" exactly. */
  static fromDecimal(text: string): Fraction {
    const match = /^(-?)(\d+)(?:\.(\d+))?$/.exec(text);
    if (!match) throw new Error(`not a finite decimal: ${text}`);
    const [, sign, intPart, fracPart = ''] = match;
    const scale = 10n ** BigInt(fracPart.length);
    const num = BigInt(intPart) * scale + BigInt(fracPart === '' ? 0 : fracPart);
    return Fraction.of(sign === '-' ? -num : num, scale);
  }

  get zero(): boolean {
    return this.num === 0n;
  }

  sign(): number {
    return this.num > 0n ? 1 : this.num < 0n ? -1 : 0;
  }

  add(other: Fraction): Fraction {
    return Fraction.of(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Fraction): Fraction {
    return Fraction.of(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Fraction): Fraction {
    return Fraction.of(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.zero) throw new Error('division by zero fraction');
    return Fraction.of(this.num * other.den, this.den * other.num);
  }

  neg(): Fraction {
    return Fraction.of(-this.num, this.den);
  }

  abs(): Fraction {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(other: Fraction): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs > rhs ? 1 : lhs < rhs ? -1 : 0;
  }

  /** Round to the nearest multiple of `step`, halves away from zero. */
  snapTo(step: Fraction): Fraction {
    if (step.zero) return this;
    const ratio = this.div(step);
    const rounded = roundHalfAwayFromZero(ratio);
    return rounded.mul(step);
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}

function absBig(value: bigint): bigint {
  return value < 0n ? -value : value;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a;
  let y = b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

function floorDiv(num: bigint, den: bigint): bigint {
  const q = num / den;
    // BigInt division truncates toward zero; adjust when the remainder is non-zero
  // and the exact result is negative.
  return num % den !== 0n && (num < 0n) !== (den < 0n) ? q - 1n : q;
}

/** Nearest integer to `value`, halves rounded away from zero. */
export function roundHalfAwayFromZero(value: Fraction): Fraction {
  const half = Fraction.of(value.sign() >= 0 ? 1 : -1, 2);
  const shifted = value.add(half);
  return Fraction.of(floorDiv(shifted.num, shifted.den), 1);
}
