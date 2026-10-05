/**
 * Exact rational arithmetic on BigInt, used by the exact (symbolic)
 * derivative-root analysis. Every value is kept reduced with a positive
 * denominator, so equality and sign tests are exact.
 */

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

export class Fraction {
  readonly n: bigint;
  readonly d: bigint;

  private constructor(n: bigint, d: bigint) {
    if (d === 0n) throw new Error('Fraction with zero denominator');
    let num = n;
    let den = d;
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    this.n = num / g;
    this.d = den / g;
  }

  static of(n: bigint | number, d: bigint | number = 1): Fraction {
    return new Fraction(BigInt(n), BigInt(d));
  }

  /** Exact rational value of a finite IEEE-754 double. */
  static fromNumber(x: number): Fraction {
    if (!Number.isFinite(x)) {
      throw new Error(`cannot convert non-finite number ${x} to a Fraction`);
    }
    if (x === 0) return new Fraction(0n, 1n);
    const view = new DataView(new ArrayBuffer(8));
    view.setFloat64(0, x);
    const bits = view.getBigUint64(0);
    const sign = (bits >> 63n) === 1n ? -1n : 1n;
    const expField = (bits >> 52n) & 0x7ffn;
    const fracField = bits & 0xfffffffffffffn;
    let mantissa: bigint;
    let exp: number;
    if (expField === 0n) {
      mantissa = fracField;
      exp = -1074;
    } else {
      mantissa = fracField | (1n << 52n);
      exp = Number(expField) - 1075;
    }
    const num = sign * mantissa;
    if (exp >= 0) return new Fraction(num << BigInt(exp), 1n);
    return new Fraction(num, 1n << BigInt(-exp));
  }

  static get zero(): Fraction {
    return new Fraction(0n, 1n);
  }

  static get one(): Fraction {
    return new Fraction(1n, 1n);
  }

  add(o: Fraction): Fraction {
    return new Fraction(this.n * o.d + o.n * this.d, this.d * o.d);
  }

  sub(o: Fraction): Fraction {
    return new Fraction(this.n * o.d - o.n * this.d, this.d * o.d);
  }

  mul(o: Fraction): Fraction {
    return new Fraction(this.n * o.n, this.d * o.d);
  }

  div(o: Fraction): Fraction {
    if (o.n === 0n) throw new Error('division by zero Fraction');
    return new Fraction(this.n * o.d, this.d * o.n);
  }

  neg(): Fraction {
    return new Fraction(-this.n, this.d);
  }

  abs(): Fraction {
    return this.n < 0n ? new Fraction(-this.n, this.d) : this;
  }

  sign(): -1 | 0 | 1 {
    return this.n < 0n ? -1 : this.n > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.n === 0n;
  }

  cmp(o: Fraction): -1 | 0 | 1 {
    const diff = this.n * o.d - o.n * this.d;
    return diff < 0n ? -1 : diff > 0n ? 1 : 0;
  }

  equals(o: Fraction): boolean {
    return this.n === o.n && this.d === o.d;
  }

  toNumber(): number {
    return Number(this.n) / Number(this.d);
  }

  toString(): string {
    return this.d === 1n ? `${this.n}` : `${this.n}/${this.d}`;
  }
}
