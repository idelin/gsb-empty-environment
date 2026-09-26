export type RationalInput = Rational | bigint | number | string;

/**
 * 精确有理数（BigInt 分子分母，始终约分、分母恒正）。
 * 滚轮增量与滚动位置全部用它表示，浮点残差不丢失：
 * 0.1 + 0.2 === 0.3，1/3 * 3 === 1，均精确成立。
 */
export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    if (den === 0n) throw new Error('Rational: zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n < 0n ? -n : n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static of(value: RationalInput): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return new Rational(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) throw new Error(`Rational: non-finite number ${value}`);
      // 走十进制短往返表示，避免二进制浮点噪声进入有理数
      return Rational.of(value.toString());
    }
    const text = value.trim();
    const slash = text.indexOf('/');
    if (slash >= 0) {
      return new Rational(BigInt(text.slice(0, slash)), BigInt(text.slice(slash + 1)));
    }
    const m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text);
    if (!m) throw new Error(`Rational: cannot parse ${JSON.stringify(value)}`);
    const [, sign, intPart, fracPart = '', expPart] = m;
    const exp = (expPart ? BigInt(expPart) : 0n) - BigInt(fracPart.length);
    let n = BigInt(intPart + fracPart);
    if (sign === '-') n = -n;
    if (exp >= 0n) return new Rational(n * 10n ** exp, 1n);
    return new Rational(n, 10n ** -exp);
  }

  static zero(): Rational {
    return ZERO;
  }

  static min(a: Rational, b: Rational): Rational {
    return a.cmp(b) <= 0 ? a : b;
  }

  static max(a: Rational, b: Rational): Rational {
    return a.cmp(b) >= 0 ? a : b;
  }

  add(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: RationalInput): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.num, this.den * o.den);
  }

  div(other: RationalInput): Rational {
    const o = Rational.of(other);
    if (o.num === 0n) throw new Error('Rational: division by zero');
    return new Rational(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(other: RationalInput): -1 | 0 | 1 {
    const o = Rational.of(other);
    const l = this.num * o.den;
    const r = o.num * this.den;
    return l < r ? -1 : l > r ? 1 : 0;
  }

  eq(other: RationalInput): boolean {
    return this.cmp(other) === 0;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** 仅用于展示；内部计算永远走精确有理数。 */
  toDecimal(places = 6): string {
    const neg = this.num < 0n;
    const a = neg ? -this.num : this.num;
    const scale = 10n ** BigInt(places);
    const scaled = (a * scale) / this.den;
    const int = scaled / scale;
    const frac = (scaled % scale).toString().padStart(places, '0');
    return `${neg ? '-' : ''}${int}.${frac}`;
  }
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

const ZERO = new Rational(0n, 1n);
