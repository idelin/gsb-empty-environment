/**
 * 精确有理数（BigInt 分子/分母，始终约分，分母恒正）。
 * 库内所有合成运算都用 Frac 完成，杜绝浮点误差。
 */

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcdBig(a: bigint, b: bigint): bigint {
  a = absBig(a);
  b = absBig(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export class Frac {
  readonly n: bigint;
  readonly d: bigint;

  private constructor(n: bigint, d: bigint) {
    this.n = n;
    this.d = d;
  }

  static of(n: bigint | number, d: bigint | number = 1): Frac {
    let num = BigInt(n);
    let den = BigInt(d);
    if (den === 0n) throw new Error("Frac: zero denominator");
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcdBig(num, den);
    return new Frac(num / g, den / g);
  }

  static readonly ZERO: Frac = Frac.of(0);
  static readonly ONE: Frac = Frac.of(1);

  add(o: Frac): Frac {
    return Frac.of(this.n * o.d + o.n * this.d, this.d * o.d);
  }

  sub(o: Frac): Frac {
    return Frac.of(this.n * o.d - o.n * this.d, this.d * o.d);
  }

  mul(o: Frac): Frac {
    return Frac.of(this.n * o.n, this.d * o.d);
  }

  div(o: Frac): Frac {
    if (o.n === 0n) throw new Error("Frac: division by zero");
    return Frac.of(this.n * o.d, this.d * o.n);
  }

  isZero(): boolean {
    return this.n === 0n;
  }

  sign(): number {
    return this.n < 0n ? -1 : this.n > 0n ? 1 : 0;
  }

  cmp(o: Frac): number {
    const lhs = this.n * o.d;
    const rhs = o.n * this.d;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  toNumber(): number {
    return Number(this.n) / Number(this.d);
  }

  toString(): string {
    return this.d === 1n ? `${this.n}` : `${this.n}/${this.d}`;
  }
}
