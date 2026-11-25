/**
 * 精确有理数：所有概率、似然、后验都用 bigint 分数表示，
 * 全程不经过浮点，因此可以可靠判断“恰好等于三倍”这类边界。
 */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint | number | string, den: bigint | number | string = 1n) {
    const n = toBigInt(num);
    const d = toBigInt(den);
    if (d === 0n) throw new Error("Fraction: 分母不能为零");
    const sign = (n < 0n) !== (d < 0n) ? -1n : 1n;
    const an = n < 0n ? -n : n;
    const ad = d < 0n ? -d : d;
    const g = gcd(an, ad);
    this.num = (an / g) * sign;
    this.den = ad / g;
  }

  static readonly ZERO = new Fraction(0n);
  static readonly ONE = new Fraction(1n);

  add(other: Fraction): Fraction {
    return new Fraction(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Fraction): Fraction {
    return new Fraction(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Fraction): Fraction {
    return new Fraction(this.num * other.num, this.den * other.den);
  }

  div(other: Fraction): Fraction {
    if (other.num === 0n) throw new Error("Fraction: 不能除以零");
    return new Fraction(this.num * other.den, this.den * other.num);
  }

  /** 除以正整数（用于平均等场景）。 */
  divBy(n: bigint | number): Fraction {
    const b = toBigInt(n);
    if (b === 0n) throw new Error("Fraction: 不能除以零");
    return new Fraction(this.num, this.den * (b < 0n ? -b : b));
  }

  negate(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  compareTo(other: Fraction): -1 | 0 | 1 {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    if (lhs < rhs) return -1;
    if (lhs > rhs) return 1;
    return 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  equals(other: Fraction): boolean {
    return this.num === other.num && this.den === other.den;
  }

  /** 精确的 a/b 字符串，永不转浮点。 */
  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }
}

function toBigInt(v: bigint | number | string): bigint {
  if (typeof v === "bigint") return v;
  if (typeof v === "number") {
    if (!Number.isFinite(v)) throw new Error("Fraction: 数值必须有限");
    return BigInt(v);
  }
  return BigInt(v);
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const r = x % y;
    x = y;
    y = r;
  }
  return x === 0n ? 1n : x;
}

export function sumFractions(parts: readonly Fraction[]): Fraction {
  let acc = Fraction.ZERO;
  for (const part of parts) acc = acc.add(part);
  return acc;
}
