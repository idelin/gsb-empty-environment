/**
 * Decimal：基于 BigInt 的精确十进制数。
 *
 * 表示：value = coeff × 10^exp（coeff 为任意精度整数，规范化后不含尾部零）。
 * 只接受 string | bigint | Decimal 构造——拒绝 number，杜绝大数经 Number 中转。
 *
 * 舍入规则（显式）：
 *  - 加、减、乘、取负：精确，无舍入。
 *  - 除法、开方：在 rational.ts 中以精确有理数计算，仅在最终输出时
 *    按指定的 RoundingMode 舍入到指定有效数字（唯一舍入点）。
 *  - roundToExp / roundToSig：按指定模式显式舍入。
 */

export type RoundingMode =
  | 'half-even' // 银行家舍入：恰为一半时向偶数靠
  | 'half-up'   // 恰为一半时远离零
  | 'down'      // 向零截断
  | 'up'        // 远离零
  | 'floor'     // 向 -∞
  | 'ceiling';  // 向 +∞

export const DEFAULT_SIG_DIGITS = 6;
export const DEFAULT_ROUNDING_MODE: RoundingMode = 'half-even';

const MAX_EXPONENT = 1_000_000;
const MAX_ALIGN_SHIFT = 200_000;

const pow10Cache: bigint[] = [1n, 10n];

export function pow10(n: number): bigint {
  if (!Number.isSafeInteger(n) || n < 0) throw new RangeError(`pow10: 非法指数 ${n}`);
  if (n < pow10Cache.length) return pow10Cache[n]!;
  let v = pow10Cache[pow10Cache.length - 1]!;
  for (let i = pow10Cache.length; i <= Math.min(n, 1024); i++) {
    v *= 10n;
    pow10Cache.push(v);
  }
  if (n < pow10Cache.length) return pow10Cache[n]!;
  return 10n ** BigInt(n);
}

export function numDigits(v: bigint): number {
  return (v < 0n ? -v : v).toString().length;
}

/**
 * 计算 round(n / d)，d > 0，按 mode 舍入。
 * sticky = true 表示商在被舍入位之后还有非零数字（用于多步计算的精确舍入传递）。
 */
export function roundRatio(n: bigint, d: bigint, mode: RoundingMode, sticky = false): bigint {
  if (d <= 0n) throw new RangeError('roundRatio: 分母必须为正');
  const q = n / d; // 向零截断
  const r = n % d; // 符号与 n 相同
  if (r === 0n && !sticky) return q;
  const sign = n < 0n ? -1n : 1n;
  const absR = r < 0n ? -r : r;
  switch (mode) {
    case 'down':
      return q;
    case 'up':
      return q + sign;
    case 'floor':
      return sign < 0n ? q - 1n : q;
    case 'ceiling':
      return sign > 0n ? q + 1n : q;
    case 'half-up':
    case 'half-even': {
      const twice = 2n * absR;
      let cmp: number;
      if (twice < d) cmp = -1;
      else if (twice > d) cmp = 1;
      else cmp = sticky ? 1 : 0; // 恰为一半但后面还有非零位 → 超过一半
      if (cmp > 0) return q + sign;
      if (cmp < 0) return q;
      if (mode === 'half-up') return q + sign;
      return q % 2n !== 0n ? q + sign : q; // 恰为一半 → 向偶数
    }
  }
}

export class Decimal {
  readonly coeff: bigint;
  readonly exp: number;

  private constructor(coeff: bigint, exp: number) {
    this.coeff = coeff;
    this.exp = exp;
  }

  static make(coeff: bigint, exp: number): Decimal {
    if (!Number.isSafeInteger(exp) || Math.abs(exp) > MAX_EXPONENT) {
      throw new RangeError(`Decimal: 指数超出允许范围: ${exp}`);
    }
    if (coeff === 0n) return new Decimal(0n, 0);
    let c = coeff;
    let e = exp;
    while (c % 10n === 0n) {
      c /= 10n;
      e += 1;
    }
    return new Decimal(c, e);
  }

  static zero(): Decimal {
    return new Decimal(0n, 0);
  }

  static one(): Decimal {
    return new Decimal(1n, 0);
  }

  /** 从十进制字符串解析（支持科学记数法）。全程 BigInt，不经 Number 中转数值。 */
  static parse(text: string): Decimal {
    const m = /^([+-]?)(\d+)?(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m) throw new Error(`无法解析的十进制数: ${JSON.stringify(text)}`);
    const intPart = m[2] ?? '';
    const fracPart = m[3] ?? '';
    if (intPart === '' && fracPart === '') {
      throw new Error(`无法解析的十进制数: ${JSON.stringify(text)}`);
    }
    const expPart = m[4] !== undefined ? Number(m[4]) : 0;
    if (!Number.isSafeInteger(expPart) || Math.abs(expPart) > MAX_EXPONENT) {
      throw new RangeError(`Decimal: 指数超出允许范围: ${m[4]}`);
    }
    const digits = (intPart + fracPart).replace(/^0+/, '') || '0';
    const coeff = BigInt((m[1] === '-' ? '-' : '') + digits);
    return Decimal.make(coeff, expPart - fracPart.length);
  }

  /** 只接受 string | bigint | Decimal；number 一律拒绝（防止大数经 Number 中转）。 */
  static from(value: string | bigint | Decimal): Decimal {
    if (value instanceof Decimal) return value;
    if (typeof value === 'bigint') return Decimal.make(value, 0);
    if (typeof value === 'string') return Decimal.parse(value);
    throw new TypeError(
      'Decimal 只接受 string | bigint | Decimal；禁止传入 number（避免大数经 Number 中转丢失精度）',
    );
  }

  get sign(): number {
    return this.coeff === 0n ? 0 : this.coeff < 0n ? -1 : 1;
  }

  isZero(): boolean {
    return this.coeff === 0n;
  }

  abs(): Decimal {
    return this.coeff < 0n ? new Decimal(-this.coeff, this.exp) : this;
  }

  neg(): Decimal {
    return new Decimal(-this.coeff, this.exp);
  }

  /** 最高有效位的十进制指数：|value| ∈ [10^oom, 10^(oom+1))。零无定义。 */
  orderOfMagnitude(): number {
    if (this.coeff === 0n) throw new RangeError('0 没有数量级');
    return this.exp + numDigits(this.coeff) - 1;
  }

  add(other: Decimal): Decimal {
    const e = Math.min(this.exp, other.exp);
    const shiftA = this.exp - e;
    const shiftB = other.exp - e;
    if (shiftA > MAX_ALIGN_SHIFT || shiftB > MAX_ALIGN_SHIFT) {
      throw new RangeError('Decimal.add: 两数数量级差异过大，无法精确对齐');
    }
    return Decimal.make(this.coeff * pow10(shiftA) + other.coeff * pow10(shiftB), e);
  }

  sub(other: Decimal): Decimal {
    return this.add(other.neg());
  }

  mul(other: Decimal): Decimal {
    return Decimal.make(this.coeff * other.coeff, this.exp + other.exp);
  }

  cmp(other: Decimal): number {
    const sa = this.sign;
    const sb = other.sign;
    if (sa !== sb) return sa < sb ? -1 : 1;
    if (sa === 0) return 0;
    const oa = this.orderOfMagnitude();
    const ob = other.orderOfMagnitude();
    if (oa !== ob) return (oa < ob ? -1 : 1) * sa;
    const e = Math.min(this.exp, other.exp);
    const ac = this.coeff * pow10(this.exp - e);
    const bc = other.coeff * pow10(other.exp - e);
    return ac === bc ? 0 : ac < bc ? -1 : 1;
  }

  equals(other: Decimal): boolean {
    return this.cmp(other) === 0;
  }

  /** 舍入到 10^targetExp 的整数倍。 */
  roundToExp(targetExp: number, mode: RoundingMode): Decimal {
    if (this.coeff === 0n || this.exp >= targetExp) return this;
    const shift = targetExp - this.exp;
    const q = roundRatio(this.coeff, pow10(shift), mode);
    return Decimal.make(q, targetExp);
  }

  /** 舍入到 sigDigits 位有效数字。sticky 见 roundRatio。 */
  roundToSig(sigDigits: number, mode: RoundingMode, sticky = false): Decimal {
    if (!Number.isSafeInteger(sigDigits) || sigDigits < 1) {
      throw new RangeError(`roundToSig: 有效数字必须为正整数: ${sigDigits}`);
    }
    if (this.coeff === 0n) return this;
    const digits = numDigits(this.coeff);
    if (digits <= sigDigits) return this;
    const targetExp = this.exp + (digits - sigDigits);
    const q = roundRatio(this.coeff, pow10(targetExp - this.exp), mode, sticky);
    return Decimal.make(q, targetExp);
  }

  /**
   * 以恰好 sigDigits 位有效数字显示（右侧补零）。
   * 仅作显示：要求数值已按不超过 sigDigits 位舍入；位数超出时原样显示。
   */
  toSigString(sigDigits: number): string {
    if (this.coeff === 0n) return '0';
    const digits = numDigits(this.coeff);
    if (digits >= sigDigits) return this.toString();
    const raw = new Decimal(
      this.coeff * pow10(sigDigits - digits),
      this.exp - (sigDigits - digits),
    );
    return raw.toString();
  }

  /** 精确十进制串（普通记法；数量级过大时用科学记数法，但保持全部有效位）。 */
  toString(): string {
    if (this.coeff === 0n) return '0';
    const neg = this.coeff < 0n;
    const digits = (neg ? -this.coeff : this.coeff).toString();
    const oom = this.exp + digits.length - 1;
    let body: string;
    if (oom > 500 || this.exp < -500) {
      body = digits.length === 1 ? digits : `${digits[0]}.${digits.slice(1)}`;
      body += `E${oom}`;
    } else if (this.exp >= 0) {
      body = digits + '0'.repeat(this.exp);
    } else {
      const point = digits.length + this.exp;
      body =
        point > 0
          ? `${digits.slice(0, point)}.${digits.slice(point)}`
          : `0.${'0'.repeat(-point)}${digits}`;
    }
    return (neg ? '-' : '') + body;
  }
}
