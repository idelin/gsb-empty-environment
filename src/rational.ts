/**
 * Rational：基于 BigInt 的精确有理数（num/den，den > 0，已约分）。
 *
 * 所有矩阵代数（方差二次型、LDL 分解、行列式）都在 Rational 上进行，
 * 全程无舍入；只有最终开方 / 显示时才按显式规则舍入到 Decimal。
 */

import { Decimal, numDigits, pow10, roundRatio, type RoundingMode } from './decimal.ts';

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

/** 比较 num/den 与 10^exp10 的大小（den > 0）。 */
function compareScaled(num: bigint, den: bigint, exp10: number): number {
  if (exp10 >= 0) {
    const rhs = den * pow10(exp10);
    return num === rhs ? 0 : num < rhs ? -1 : 1;
  }
  const lhs = num * pow10(-exp10);
  return lhs === den ? 0 : lhs < den ? -1 : 1;
}

/** 精确整数平方根：floor(√n)。 */
export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new RangeError('isqrt: 负数');
  if (n < 2n) return n;
  const bitLen = n.toString(2).length;
  let x = 1n << BigInt(Math.ceil(bitLen / 2) + 1);
  for (;;) {
    const y = (x + n / x) >> 1n;
    if (y >= x) return x;
    x = y;
  }
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint; // 恒 > 0，且 gcd(|num|, den) = 1

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static make(num: bigint, den: bigint): Rational {
    if (den === 0n) throw new RangeError('Rational: 分母为零');
    if (num === 0n) return new Rational(0n, 1n);
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    return new Rational(n / g, d / g);
  }

  static zero(): Rational {
    return new Rational(0n, 1n);
  }

  static one(): Rational {
    return new Rational(1n, 1n);
  }

  static from(d: Decimal): Rational {
    if (d.exp >= 0) return Rational.make(d.coeff * pow10(d.exp), 1n);
    return Rational.make(d.coeff, pow10(-d.exp));
  }

  get sign(): number {
    return this.num === 0n ? 0 : this.num < 0n ? -1 : 1;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? new Rational(-this.num, this.den) : this;
  }

  add(o: Rational): Rational {
    return Rational.make(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return Rational.make(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rational): Rational {
    return Rational.make(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.isZero()) throw new RangeError('Rational.div: 除数为零');
    return Rational.make(this.num * o.den, this.den * o.num);
  }

  cmp(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs === rhs ? 0 : lhs < rhs ? -1 : 1;
  }

  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** 若分母只含因子 2 和 5（十进制有限），返回精确 Decimal；否则 null。 */
  toDecimalExact(): Decimal | null {
    let d = this.den;
    let twos = 0;
    let fives = 0;
    while (d % 2n === 0n) {
      d /= 2n;
      twos += 1;
    }
    while (d % 5n === 0n) {
      d /= 5n;
      fives += 1;
    }
    if (d !== 1n) return null;
    const m = Math.max(twos, fives);
    const coeff = this.num * 2n ** BigInt(m - twos) * 5n ** BigInt(m - fives);
    return Decimal.make(coeff, -m);
  }

  /** 正确舍入到 sigDigits 位有效数字的 Decimal（单次舍入，无双重舍入误差）。 */
  toDecimal(sigDigits: number, mode: RoundingMode): Decimal {
    if (this.num === 0n) return Decimal.zero();
    const an = this.num < 0n ? -this.num : this.num;
    // 精确确定 E 使 10^E ≤ |value| < 10^(E+1)
    let e = numDigits(an) - numDigits(this.den);
    if (compareScaled(an, this.den, e) < 0) e -= 1;
    const targetExp = e - sigDigits + 1;
    let n: bigint;
    let d: bigint;
    if (targetExp >= 0) {
      n = this.num;
      d = this.den * pow10(targetExp);
    } else {
      n = this.num * pow10(-targetExp);
      d = this.den;
    }
    const q = roundRatio(n, d, mode);
    return Decimal.make(q, targetExp);
  }

  /** √this 正确舍入到 sigDigits 位有效数字（比较与舍入判定全部精确）。 */
  sqrt(sigDigits: number, mode: RoundingMode): Decimal {
    if (this.sign < 0) throw new RangeError('Rational.sqrt: 负数无实数平方根');
    if (this.isZero()) return Decimal.zero();
    // 精确确定 E 使 10^(2E) ≤ R < 10^(2E+2)
    let e = Math.floor((numDigits(this.num) - numDigits(this.den)) / 2);
    while (compareScaled(this.num, this.den, 2 * e) < 0) e -= 1;
    while (compareScaled(this.num, this.den, 2 * (e + 1)) >= 0) e += 1;
    const t = e - sigDigits + 1; // 目标舍入位：10^t
    // n = floor(√R / 10^t) = isqrt(floor(num·10^max(-2t,0) / (den·10^max(2t,0))))
    const a = t <= 0 ? this.num * pow10(-2 * t) : this.num;
    const b = t >= 0 ? this.den * pow10(2 * t) : this.den;
    const n = isqrt(a / b);
    const exact = n * n * b === a;
    // 比较 R 与 ((n + 1/2)·10^t)^2，即 4·num·10^max(-2t,0) ? (2n+1)^2·den·10^max(2t,0)
    const half = (2n * n + 1n) ** 2n;
    const left = 4n * this.num * (t <= 0 ? pow10(-2 * t) : 1n);
    const right = half * this.den * (t >= 0 ? pow10(2 * t) : 1n);
    const cmp = left === right ? 0 : left < right ? -1 : 1;
    let rounded: bigint;
    switch (mode) {
      case 'down':
      case 'floor':
        rounded = n;
        break;
      case 'up':
      case 'ceiling':
        rounded = exact ? n : n + 1n;
        break;
      case 'half-up':
        rounded = cmp >= 0 ? n + 1n : n;
        break;
      case 'half-even':
        rounded = cmp > 0 ? n + 1n : cmp < 0 ? n : n % 2n !== 0n ? n + 1n : n;
        break;
    }
    return Decimal.make(rounded, t);
  }
}

/** 两个 Decimal 的除法，正确舍入到 sigDigits 位有效数字。 */
export function divDecimal(
  a: Decimal,
  b: Decimal,
  sigDigits: number,
  mode: RoundingMode,
): Decimal {
  if (b.isZero()) throw new RangeError('divDecimal: 除数为零');
  return Rational.from(a).div(Rational.from(b)).toDecimal(sigDigits, mode);
}

/** Decimal 的平方根，正确舍入到 sigDigits 位有效数字。 */
export function sqrtDecimal(d: Decimal, sigDigits: number, mode: RoundingMode): Decimal {
  return Rational.from(d).sqrt(sigDigits, mode);
}
