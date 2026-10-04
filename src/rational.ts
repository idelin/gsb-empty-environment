/**
 * 基于 BigInt 的精确有理数运算。
 * 所有运算保持最简分数形式，避免浮点误差。
 */

export type RationalLike = Rational | string | number | bigint;

function absBig(x: bigint): bigint {
  return x < 0n ? -x : x;
}

function gcdBig(a: bigint, b: bigint): bigint {
  a = absBig(a);
  b = absBig(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

const DECIMAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

function decimalToRational(text: string): Rational {
  const m = DECIMAL_RE.exec(text);
  if (!m || ((m[2] ?? "") === "" && (m[3] ?? "") === "")) {
    throw new Error(`无法解析为有理数: "${text}"`);
  }
  const sign = m[1] === "-" ? "-" : "";
  const intPart = m[2] ?? "";
  const fracPart = m[3] ?? "";
  const exp = m[4] ? BigInt(m[4]) : 0n;
  const digits = (intPart + fracPart).replace(/^0+(?=\d)/, "") || "0";
  let num = BigInt(sign + digits);
  let den = 1n;
  const scale = BigInt(fracPart.length) - exp;
  if (scale >= 0n) {
    den = 10n ** scale;
  } else {
    num *= 10n ** -scale;
  }
  return Rational.from(num, den);
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    this.num = num;
    this.den = den;
  }

  static readonly ZERO: Rational = new Rational(0n, 1n);
  static readonly ONE: Rational = new Rational(1n, 1n);

  /** 由分子/分母构造并约分。 */
  static from(num: bigint, den: bigint = 1n): Rational {
    if (den === 0n) throw new Error("分母不能为 0");
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcdBig(num, den);
    return new Rational(num / g, den / g);
  }

  static of(value: RationalLike): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === "bigint") return Rational.from(value);
    if (typeof value === "number") {
      if (!Number.isFinite(value)) throw new Error(`非法数值: ${value}`);
      return decimalToRational(String(value));
    }
    return Rational.parse(value);
  }

  /** 支持 "3/4"、"0.75"、"12"、"1e-3" 等写法。 */
  static parse(text: string): Rational {
    const s = text.trim();
    const slash = s.indexOf("/");
    if (slash >= 0) {
      const num = decimalToRational(s.slice(0, slash).trim());
      const den = decimalToRational(s.slice(slash + 1).trim());
      return num.div(den);
    }
    return decimalToRational(s);
  }

  add(o: Rational): Rational {
    return Rational.from(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Rational): Rational {
    return Rational.from(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Rational): Rational {
    if (this.isZero() || o.isZero()) return Rational.ZERO;
    return Rational.from(this.num * o.num, this.den * o.den);
  }

  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("除以零");
    return Rational.from(this.num * o.den, this.den * o.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  cmp(o: Rational): number {
    const lhs = this.num * o.den;
    const rhs = o.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  lt(o: Rational): boolean {
    return this.cmp(o) < 0;
  }

  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isOne(): boolean {
    return this.num === this.den;
  }

  /** 精确分数形式，如 "3/4"；整数只输出分子。 */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** 十进制近似（四舍五入到 places 位）。 */
  toDecimal(places = 6): string {
    const neg = this.num < 0n;
    const a = absBig(this.num);
    const scale = 10n ** BigInt(places);
    const prod = a * scale;
    let q = prod / this.den;
    const r = prod % this.den;
    if (r * 2n >= this.den) q += 1n;
    const intPart = q / scale;
    const fracDigits = (q % scale).toString().padStart(places, "0").replace(/0+$/, "");
    return (neg ? "-" : "") + intPart.toString() + (fracDigits ? "." + fracDigits : "");
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }
}
