const gcd = (a: bigint, b: bigint): bigint => {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
};

/** 精确有理数，始终以约分形式保存且分母为正。 */
export class Rat {
  readonly n: bigint;
  readonly d: bigint;

  private constructor(n: bigint, d: bigint) {
    this.n = n;
    this.d = d;
  }

  static of(n: bigint, d: bigint = 1n): Rat {
    if (d === 0n) throw new RangeError("rational denominator is zero");
    let num = n;
    let den = d;
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    return new Rat(num / g, den / g);
  }

  static zero(): Rat {
    return Rat.of(0n);
  }

  add(other: Rat): Rat {
    return Rat.of(this.n * other.d + other.n * this.d, this.d * other.d);
  }

  sub(other: Rat): Rat {
    return Rat.of(this.n * other.d - other.n * this.d, this.d * other.d);
  }

  mul(other: Rat): Rat {
    return Rat.of(this.n * other.n, this.d * other.d);
  }

  div(other: Rat): Rat {
    if (other.n === 0n) throw new RangeError("division by zero rational");
    return Rat.of(this.n * other.d, this.d * other.n);
  }

  scale(k: bigint): Rat {
    return Rat.of(this.n * k, this.d);
  }

  cmp(other: Rat): number {
    const lhs = this.n * other.d;
    const rhs = other.n * this.d;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  isPositive(): boolean {
    return this.n > 0n;
  }

  floor(): bigint {
    const q = this.n / this.d;
    return this.n % this.d !== 0n && this.n < 0n ? q - 1n : q;
  }

  toString(): string {
    return `${this.n}/${this.d}`;
  }

  toNumber(): number {
    return Number(this.n) / Number(this.d);
  }
}
