/**
 * BigInt 有理数：规范化存储、四则运算、交叉乘比较、
 * 带循环节与预算的十进制展开、显式舍入。
 * 全程仅使用 BigInt，不经过 Number 中转大数。
 */

export type RoundingMode = 'trunc' | 'floor' | 'ceil' | 'half-up' | 'half-even';

export interface DecimalBudget {
  /** 小数部分最多产出的位数（余数状态数上界），超出则标记未完成 */
  maxDigits: number;
}

export interface DecimalExpansion {
  negative: boolean;
  /** 整数部分十进制串（不含符号） */
  integer: string;
  /** 不循环前缀 */
  prefix: string;
  /** 循环节；有限小数时为 '' */
  period: string;
  /** false 表示超出预算，prefix 仅为截断部分，不能当作精确表示 */
  complete: boolean;
  /** 实际产出的小数位数 */
  digitsEmitted: number;
}

export interface FractionPage {
  page: number;
  pageSize: number;
  /** 本页小数位（位置 [page*pageSize, (page+1)*pageSize)） */
  digits: string;
  /** 是否还有后续位（无限循环恒为 true；有限小数超出后为 false） */
  hasMore: boolean;
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export function bitLength(x: bigint): number {
  const v = x < 0n ? -x : x;
  return v === 0n ? 1 : v.toString(2).length;
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint; // 恒 > 0

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new RangeError('rational: denominator must not be zero');
    if (num === 0n) {
      // 0 统一规范为 0/1
      this.num = 0n;
      this.den = 1n;
      return;
    }
    let n = num;
    let d = den;
    if (d < 0n) {
      // 分母为负时把符号移到分子
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static of(num: bigint | number, den: bigint | number = 1n): Rational {
    return new Rational(BigInt(num), BigInt(den));
  }

  get sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  add(o: Rational): Rational {
    return new Rational(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return new Rational(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rational): Rational {
    return new Rational(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new RangeError('rational: division by zero');
    return new Rational(this.num * o.den, this.den * o.num);
  }

  /** 交叉乘比较：BigInt 无溢出，结果安全精确 */
  cmp(o: Rational): -1 | 0 | 1 {
    const l = this.num * o.den;
    const r = o.num * this.den;
    return l < r ? -1 : l > r ? 1 : 0;
  }

  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * 十进制展开，余数状态表检测循环节。
   * 余数最多 den-1 个非零状态，必然终止或入环；
   * maxDigits 预算限制状态表规模，超出即停止并标记 complete=false，
   * 不会无限存储。
   */
  toDecimal(budget: DecimalBudget = { maxDigits: 10000 }): DecimalExpansion {
    const negative = this.num < 0n;
    const n = negative ? -this.num : this.num;
    const d = this.den;
    const integer = (n / d).toString();
    let rem = n % d;
    const seen = new Map<bigint, number>();
    let digits = '';
    while (rem !== 0n) {
      const at = seen.get(rem);
      if (at !== undefined) {
        return {
          negative,
          integer,
          prefix: digits.slice(0, at),
          period: digits.slice(at),
          complete: true,
          digitsEmitted: digits.length,
        };
      }
      if (digits.length >= budget.maxDigits) {
        return {
          negative,
          integer,
          prefix: digits,
          period: '',
          complete: false,
          digitsEmitted: digits.length,
        };
      }
      seen.set(rem, digits.length);
      rem *= 10n;
      digits += (rem / d).toString();
      rem %= d;
    }
    return { negative, integer, prefix: digits, period: '', complete: true, digitsEmitted: digits.length };
  }

  /** 形如 0.1(6)、-2.(142857)、0.125；未完成时以 … 结尾且不得视为精确值 */
  toDecimalString(budget?: DecimalBudget): string {
    return formatDecimal(this.toDecimal(budget));
  }

  /**
   * 显式舍入的定点输出。结果是舍入后的近似串，不是精确表示。
   * trunc 向零、floor 向 -inf、ceil 向 +inf、half-up 四舍五入（远离零）、
   * half-even 银行家舍入。
   */
  toFixed(places: number, mode: RoundingMode = 'half-up'): string {
    if (!Number.isSafeInteger(places) || places < 0) {
      throw new RangeError('toFixed: places must be a non-negative safe integer');
    }
    const scale = 10n ** BigInt(places);
    const scaled = this.num * scale;
    let q = scaled / this.den; // BigInt 除法向零截断
    const r = scaled - q * this.den;
    if (r !== 0n) {
      const absR = r < 0n ? -r : r;
      const away = scaled > 0n ? 1n : -1n;
      switch (mode) {
        case 'trunc':
          break;
        case 'floor':
          if (scaled < 0n) q -= 1n;
          break;
        case 'ceil':
          if (scaled > 0n) q += 1n;
          break;
        case 'half-up':
          if (2n * absR >= this.den) q += away;
          break;
        case 'half-even':
          if (2n * absR > this.den || (2n * absR === this.den && q % 2n !== 0n)) q += away;
          break;
      }
    }
    const negative = q < 0n;
    const s = (negative ? -q : q).toString().padStart(places + 1, '0');
    const intPart = s.slice(0, s.length - places);
    const fracPart = s.slice(s.length - places);
    return (negative ? '-' : '') + intPart + (places > 0 ? '.' + fracPart : '');
  }
}

export function formatDecimal(e: DecimalExpansion): string {
  const sign = e.negative && (e.integer !== '0' || e.prefix !== '' || e.period !== '') ? '-' : '';
  if (!e.complete) return `${sign}${e.integer}.${e.prefix}…[incomplete]`;
  if (e.period === '') return e.prefix === '' ? `${sign}${e.integer}` : `${sign}${e.integer}.${e.prefix}`;
  return `${sign}${e.integer}.${e.prefix}(${e.period})`;
}

/** 由 (符号, 整数, 前缀, 循环节) 精确还原有理数，用于循环节回转校验 */
export function fromDecimalParts(
  negative: boolean,
  integer: string,
  prefix: string,
  period: string,
): Rational {
  for (const [name, s] of [['integer', integer], ['prefix', prefix], ['period', period]] as const) {
    if (!/^\d*$/.test(s)) throw new RangeError(`fromDecimalParts: bad ${name}`);
  }
  const intNum = integer === '' ? 0n : BigInt(integer);
  if (period === '') {
    const den = 10n ** BigInt(prefix.length);
    const frac = prefix === '' ? 0n : BigInt(prefix);
    const num = intNum * den + frac;
    return new Rational(negative ? -num : num, den);
  }
  const p = prefix === '' ? 0n : BigInt(prefix);
  const pq = BigInt(prefix + period);
  const nines = 10n ** BigInt(period.length) - 1n;
  const den = nines * 10n ** BigInt(prefix.length);
  const num = intNum * den + (pq - p);
  return new Rational(negative ? -num : num, den);
}

/** 小数部分第 k 位（0 起），有限小数超出后补 '0'；要求展开已完成 */
export function fractionDigitAt(e: DecimalExpansion, k: number): string {
  if (!e.complete) throw new RangeError('fractionDigitAt: expansion incomplete');
  if (k < e.prefix.length) return e.prefix[k]!;
  if (e.period === '') return '0';
  return e.period[(k - e.prefix.length) % e.period.length]!;
}

/** 分页输出小数位，无需展开巨大十进制串即可取任意页 */
export function fractionPage(e: DecimalExpansion, page: number, pageSize: number): FractionPage {
  if (!Number.isSafeInteger(page) || page < 0 || !Number.isSafeInteger(pageSize) || pageSize <= 0) {
    throw new RangeError('fractionPage: bad page/pageSize');
  }
  let digits = '';
  const start = page * pageSize;
  for (let i = 0; i < pageSize; i++) digits += fractionDigitAt(e, start + i);
  const hasMore = e.period !== '' || start + pageSize < e.prefix.length;
  return { page, pageSize, digits, hasMore };
}
