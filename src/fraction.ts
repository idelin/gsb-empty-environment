export type FracInput = Frac | string | number | bigint;

function abs(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

/** 约分后的整数分数，全程 bigint 运算，不存在浮点误差。 */
export class Frac {
  readonly num: bigint;
  readonly den: bigint; // 恒 > 0，且 gcd(|num|, den) = 1

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new Error("分数的分母不能为 0");
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(abs(num), den);
    this.num = num / g;
    this.den = den / g;
  }

  static of(input: FracInput): Frac {
    if (input instanceof Frac) return input;
    if (typeof input === "bigint") return new Frac(input, 1n);
    if (typeof input === "number") {
      if (!Number.isInteger(input)) {
        throw new Error(`拒绝浮点数输入: ${input}，请改用 "p/q" 或十进制字符串`);
      }
      return new Frac(BigInt(input), 1n);
    }
    return Frac.parse(input);
  }

  /** 支持 "p/q"、整数、十进制字符串（如 "0.25" 精确等于 1/4）。 */
  static parse(text: string): Frac {
    const s = text.trim();
    let m = /^([+-]?\d+)\/(\d+)$/.exec(s);
    if (m) return new Frac(BigInt(m[1]), BigInt(m[2]));
    m = /^([+-]?)(\d*)\.(\d+)$/.exec(s);
    if (m) {
      const digits = m[3];
      const den = 10n ** BigInt(digits.length);
      const num = BigInt((m[1] === "-" ? "-" : "") + (m[2] || "0") + digits);
      return new Frac(num, den);
    }
    if (/^[+-]?\d+$/.test(s)) return new Frac(BigInt(s), 1n);
    throw new Error(`无法解析分数: ${text}`);
  }

  add(other: FracInput): Frac {
    const b = Frac.of(other);
    return new Frac(this.num * b.den + b.num * this.den, this.den * b.den);
  }

  sub(other: FracInput): Frac {
    const b = Frac.of(other);
    return new Frac(this.num * b.den - b.num * this.den, this.den * b.den);
  }

  mul(other: FracInput): Frac {
    const b = Frac.of(other);
    return new Frac(this.num * b.num, this.den * b.den);
  }

  div(other: FracInput): Frac {
    const b = Frac.of(other);
    if (b.num === 0n) throw new Error("除数不能为 0");
    return new Frac(this.num * b.den, this.den * b.num);
  }

  cmp(other: FracInput): number {
    const b = Frac.of(other);
    const l = this.num * b.den;
    const r = b.num * this.den;
    return l < r ? -1 : l > r ? 1 : 0;
  }

  equals(other: FracInput): boolean {
    return this.cmp(other) === 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isNegative(): boolean {
    return this.num < 0n;
  }

  toString(): string {
    return this.den === 1n ? `${this.num}` : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}
