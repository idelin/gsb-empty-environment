/**
 * 精确有理数：以 BigInt 分子/分母表示，全程不经过 Number 中转，
 * 因此任意大的整数与任意精确的十进制小数都不会丢失精度。
 *
 * 只有在显式调用 toDecimalString(places, mode) 时才发生舍入，
 * 舍入规则由调用方显式指定。
 */

export type RoundingMode =
  | 'half-even' // 银行家舍入：恰好一半时向偶数靠
  | 'half-up' // 一半时远离零
  | 'half-down' // 一半时向零靠
  | 'up' // 远离零
  | 'down' // 向零截断
  | 'floor' // 向负无穷
  | 'ceiling'; // 向正无穷

export type RationalLike = Rational | bigint | number | string;

function bigAbs(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function bigGcd(a: bigint, b: bigint): bigint {
  a = bigAbs(a);
  b = bigAbs(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

const DECIMAL_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new RangeError('分母不能为 0');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = bigGcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static readonly zero: Rational = new Rational(0n, 1n);
  static readonly one: Rational = new Rational(1n, 1n);

  /**
   * 从 bigint / 安全整数 number / 十进制字符串构造。
   * 字符串支持 "123"、"-0.01"、"1.5e-3" 以及分数形式 "5/9"。
   * 非安全整数的 number 会被拒绝，避免大数经 Number 中转丢失精度。
   */
  static of(value: RationalLike): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return new Rational(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isSafeInteger(value)) {
        throw new TypeError(
          `拒绝非安全整数的 number 输入（${value}）：大数/小数请改用字符串，避免经 Number 中转丢失精度`,
        );
      }
      return new Rational(BigInt(value), 1n);
    }
    const text = value.trim();
    const slash = text.indexOf('/');
    if (slash >= 0) {
      const numerator = Rational.of(text.slice(0, slash));
      const denominator = Rational.of(text.slice(slash + 1));
      return numerator.div(denominator);
    }
    const m = DECIMAL_RE.exec(text);
    if (!m) throw new TypeError(`无法解析为精确十进制: ${JSON.stringify(value)}`);
    const [, sign, intPart, fracPart = '', expPart] = m;
    let result = new Rational(
      BigInt(`${sign}${intPart}${fracPart}`),
      10n ** BigInt(fracPart.length),
    );
    if (expPart !== undefined) {
      const e = BigInt(expPart);
      const pow = new Rational(10n ** bigAbs(e), 1n);
      result = e >= 0n ? result.mul(pow) : result.div(pow);
    }
    return result;
  }

  add(other: RationalLike): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(other: RationalLike): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(other: RationalLike): Rational {
    const o = Rational.of(other);
    return new Rational(this.num * o.num, this.den * o.den);
  }

  div(other: RationalLike): Rational {
    const o = Rational.of(other);
    if (o.num === 0n) throw new RangeError('除以零');
    return new Rational(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  reciprocal(): Rational {
    if (this.num === 0n) throw new RangeError('零没有倒数');
    return new Rational(this.den, this.num);
  }

  compare(other: RationalLike): number {
    const o = Rational.of(other);
    const diff = this.num * o.den - o.num * this.den;
    return diff < 0n ? -1 : diff > 0n ? 1 : 0;
  }

  equals(other: RationalLike): boolean {
    const o = Rational.of(other);
    return this.num === o.num && this.den === o.den;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  /** 精确分数形式，如 "1/100"；整数则输出 "3"。 */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * 按显式舍入规则输出十进制字符串。
   * 内部用 BigInt 计算 10^places 缩放后的商与余数，舍入判定完全精确。
   */
  toDecimalString(places: number, mode: RoundingMode = 'half-even'): string {
    if (!Number.isSafeInteger(places) || places < 0) {
      throw new RangeError(`小数位数必须是非负安全整数，收到: ${places}`);
    }
    const negative = this.num < 0n;
    const magnitude = negative ? -this.num : this.num;
    const scaled = magnitude * 10n ** BigInt(places);
    let quotient = scaled / this.den;
    const remainder = scaled % this.den;

    let roundUp = false;
    if (remainder !== 0n) {
      const twice = 2n * remainder;
      switch (mode) {
        case 'up':
          roundUp = true;
          break;
        case 'down':
          roundUp = false;
          break;
        case 'ceiling':
          roundUp = !negative;
          break;
        case 'floor':
          roundUp = negative;
          break;
        case 'half-up':
          roundUp = twice >= this.den;
          break;
        case 'half-down':
          roundUp = twice > this.den;
          break;
        case 'half-even':
          roundUp = twice > this.den || (twice === this.den && quotient % 2n === 1n);
          break;
        default:
          throw new TypeError(`未知舍入规则: ${String(mode)}`);
      }
    }
    if (roundUp) quotient += 1n;

    const digits = quotient.toString().padStart(places + 1, '0');
    const intPart = places === 0 ? digits : digits.slice(0, -places);
    const fracPart = places === 0 ? '' : `.${digits.slice(-places)}`;
    const signPrefix = negative && quotient !== 0n ? '-' : '';
    return `${signPrefix}${intPart}${fracPart}`;
  }
}
