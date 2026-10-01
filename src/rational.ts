/**
 * 精确有理数：以 BigInt 分子/分母表示，全程不经过 Number 中转大数。
 * 不变式：den > 0，gcd(|num|, den) = 1，0 表示为 0/1。
 */

/** 显式舍入规则（用于把精确有理数格式化为十进制字符串）。 */
export type RoundingMode =
  | 'half-even' // 平局时向偶数（银行家舍入，默认）
  | 'half-up' //   平局时远离零
  | 'half-down' //  平局时向零
  | 'down' //       向零截断
  | 'up' //         远离零进位
  | 'floor' //      向负无穷
  | 'ceil'; //      向正无穷

export type RationalInput = Rational | bigint | string | number;

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

/** 整数平方根（向下取整），牛顿迭代。 */
export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new RangeError('isqrt: negative input');
  if (n < 2n) return n;
  let x = n;
  let y = (x + 1n) >> 1n;
  while (y < x) {
    x = y;
    y = (x + n / x) >> 1n;
  }
  return x;
}

const DECIMAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new RangeError('Rational: zero denominator');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    if (num === 0n) {
      den = 1n;
    } else {
      const g = gcd(num, den);
      num /= g;
      den /= g;
    }
    this.num = num;
    this.den = den;
  }

  static get zero(): Rational {
    return new Rational(0n);
  }
  static get one(): Rational {
    return new Rational(1n);
  }

  /**
   * 从输入构造精确有理数。
   * - string：精确解析十进制字面量，支持符号、小数点与科学计数法（如 "-1.25e30"），不经过 Number。
   * - bigint：直接使用。
   * - number：仅接受安全整数，否则抛错（防止大数经 Number 丢失精度）。
   */
  static from(input: RationalInput): Rational {
    if (input instanceof Rational) return input;
    if (typeof input === 'bigint') return new Rational(input);
    if (typeof input === 'number') {
      if (!Number.isSafeInteger(input)) {
        throw new TypeError(
          `Unsafe number ${input}: pass a decimal string or bigint to keep exactness`,
        );
      }
      return new Rational(BigInt(input));
    }
    return Rational.parse(input);
  }

  /** 精确解析十进制字符串（含科学计数法），不做任何浮点中转。 */
  static parse(text: string): Rational {
    const src = text.trim();
    const m = DECIMAL_RE.exec(src);
    if (
      !m ||
      (m[2] === '' && (m[3] === undefined || m[3] === '')) // 拒绝 ""、"."、"+"
    ) {
      throw new SyntaxError(`Invalid decimal literal: ${JSON.stringify(text)}`);
    }
    const [, sign, intPart, fracPart = '', expPart] = m;
    const digits = (intPart + fracPart).replace(/^0+(?=\d)/, '');
    let coefficient = BigInt(digits === '' ? '0' : digits);
    if (sign === '-') coefficient = -coefficient;
    const exponent = BigInt(expPart ?? '0') - BigInt(fracPart.length);
    if (exponent >= 0n) {
      return new Rational(coefficient * 10n ** exponent);
    }
    return new Rational(coefficient, 10n ** -exponent);
  }

  add(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.den + r.num * this.den, this.den * r.den);
  }
  sub(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.den - r.num * this.den, this.den * r.den);
  }
  mul(o: RationalInput): Rational {
    const r = Rational.from(o);
    return new Rational(this.num * r.num, this.den * r.den);
  }
  div(o: RationalInput): Rational {
    const r = Rational.from(o);
    if (r.num === 0n) throw new RangeError('Rational: division by zero');
    return new Rational(this.num * r.den, this.den * r.num);
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
  compare(o: RationalInput): number {
    const r = Rational.from(o);
    const d = this.num * r.den - r.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }
  equals(o: RationalInput): boolean {
    const r = Rational.from(o);
    return this.num === r.num && this.den === r.den;
  }

  /** 规范化形式，如 "3/2"、"-5"。可作为 Map 键。 */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * 按显式舍入规则格式化为固定位数的十进制字符串。
   * 舍入在 BigInt 上精确完成（比较 2*余数 与 分母），无浮点误差。
   * 结果为 0 时不输出负号（无 "-0"）。
   */
  toDecimalString(places: number, mode: RoundingMode = 'half-even'): string {
    if (!Number.isInteger(places) || places < 0) {
      throw new RangeError(`places must be a non-negative integer, got ${places}`);
    }
    const negative = this.num < 0n;
    const scaled = (negative ? -this.num : this.num) * 10n ** BigInt(places);
    let q = scaled / this.den;
    const r = scaled % this.den;
    if (r !== 0n && shouldIncrement(q, r, this.den, mode, negative)) {
      q += 1n;
    }
    let digits = q.toString();
    if (places > 0) {
      digits = digits.padStart(places + 1, '0');
      const split = digits.length - places;
      digits = `${digits.slice(0, split)}.${digits.slice(split)}`;
    }
    return negative && q !== 0n ? `-${digits}` : digits;
  }
}

/** 在绝对值域上判断余数部分是否导致进位；floor/ceil 结合符号换算。 */
function shouldIncrement(
  q: bigint,
  r: bigint,
  den: bigint,
  mode: RoundingMode,
  negative: boolean,
): boolean {
  switch (mode) {
    case 'down':
      return false;
    case 'up':
      return true; // 调用方保证 r !== 0
    case 'floor':
      return negative;
    case 'ceil':
      return !negative;
    case 'half-up':
      return 2n * r >= den;
    case 'half-down':
      return 2n * r > den;
    case 'half-even': {
      const twice = 2n * r;
      return twice > den || (twice === den && (q & 1n) === 1n);
    }
  }
}
