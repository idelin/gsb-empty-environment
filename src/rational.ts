/**
 * 精确有理数：基于 BigInt 的分子/分母表示，全程无精度损失。
 * 不变式：den > 0，gcd(|num|, den) = 1，0 表示为 0/1。
 */

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export type RationalLike = Rational | bigint | number | string;

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new Error('Rational: denominator must not be zero');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    if (num === 0n) {
      this.num = 0n;
      this.den = 1n;
      return;
    }
    const g = gcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static readonly ZERO: Rational = new Rational(0n);
  static readonly ONE: Rational = new Rational(1n);

  static from(value: RationalLike): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return new Rational(value);
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) throw new Error(`Rational: invalid number ${value}`);
      if (Number.isInteger(value)) return new Rational(BigInt(value));
      // 将有限小数精确转换为分数（基于其十进制字符串表示，避免二进制浮点误差）
      return Rational.from(value.toString());
    }
    const s = value.trim();
    if (/^[+-]?\d+$/.test(s)) return new Rational(BigInt(s));
    const frac = s.match(/^([+-]?\d+)\s*\/\s*(\d+)$/);
    if (frac) return new Rational(BigInt(frac[1]), BigInt(frac[2]));
    const dec = s.match(/^([+-]?)(\d*)\.(\d+)$/);
    if (dec) {
      const intPart = dec[2] === '' ? '0' : dec[2];
      const digits = intPart + dec[3];
      const scale = 10n ** BigInt(dec[3].length);
      const v = new Rational(BigInt(digits), scale);
      return dec[1] === '-' ? v.neg() : v;
    }
    throw new Error(`Rational: cannot parse "${value}"`);
  }

  add(other: RationalLike): Rational {
    const o = Rational.from(other);
    return new Rational(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: RationalLike): Rational {
    const o = Rational.from(other);
    return new Rational(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: RationalLike): Rational {
    const o = Rational.from(other);
    return new Rational(this.num * o.num, this.den * o.den);
  }

  div(other: RationalLike): Rational {
    const o = Rational.from(other);
    if (o.num === 0n) throw new Error('Rational: division by zero');
    return new Rational(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  equals(other: RationalLike): boolean {
    const o = Rational.from(other);
    return this.num === o.num && this.den === o.den;
  }

  /** 是否为整数值（分母为 1） */
  isInteger(): boolean {
    return this.den === 1n;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** 仅供展示/对照的浮点近似值，不参与任何计算 */
  toFloat(): number {
    return Number(this.num) / Number(this.den);
  }
}
