/**
 * 精确有理数：以 BigInt 分子/分母表示，绝不经过 Number 中转大数。
 * 所有舍入规则显式，见 roundRat。
 */

export type RoundRule =
  | 'half-up'    // 四舍五入：半数远离零
  | 'half-even'  // 银行家舍入：半数向偶
  | 'half-down'  // 半数朝向零
  | 'down'       // 截断（朝向零）
  | 'up'         // 远离零
  | 'floor'      // 向负无穷
  | 'ceil';      // 向正无穷

const abs = (x: bigint): bigint => (x < 0n ? -x : x);

function gcd(a: bigint, b: bigint): bigint {
  a = abs(a);
  b = abs(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

function toBig(x: bigint | number): bigint {
  if (typeof x === 'bigint') return x;
  if (!Number.isSafeInteger(x)) {
    throw new Error(
      `拒绝不安全的 Number → BigInt 转换: ${x}（超出安全整数范围会丢失精度，请改用字符串或 BigInt）`,
    );
  }
  return BigInt(x);
}

export class Rat {
  readonly n: bigint; // 分子（符号在分子上）
  readonly d: bigint; // 分母（恒正，已与分子约分）

  private constructor(n: bigint, d: bigint) {
    this.n = n;
    this.d = d;
  }

  static of(n: bigint | number, d: bigint | number = 1n): Rat {
    const nn = toBig(n);
    const dd = toBig(d);
    if (dd === 0n) throw new Error('Rat: 分母为零');
    const g = gcd(nn, dd);
    let rn = nn / g;
    let rd = dd / g;
    if (rd < 0n) {
      rn = -rn;
      rd = -rd;
    }
    return new Rat(rn, rd);
  }

  /** 解析 "p/q" 或十进制字符串（如 "-12.345"），全程精确。 */
  static parse(s: string): Rat {
    const t = s.trim();
    if (t.includes('/')) {
      const parts = t.split('/');
      if (parts.length !== 2) throw new Error(`无法解析有理数: ${s}`);
      return Rat.of(BigInt(parts[0]), BigInt(parts[1]));
    }
    const m = /^([+-]?)(\d*)(?:\.(\d+))?$/.exec(t);
    if (!m || (m[2] === '' && m[3] === undefined)) {
      throw new Error(`无法解析十进制: ${s}`);
    }
    const intPart = m[2] === '' ? '0' : m[2];
    const fracPart = m[3] ?? '';
    const den = 10n ** BigInt(fracPart.length);
    const num = BigInt(intPart) * den + (fracPart === '' ? 0n : BigInt(fracPart));
    return Rat.of(m[1] === '-' ? -num : num, den);
  }

  static zero(): Rat {
    return Rat.of(0n);
  }
  static one(): Rat {
    return Rat.of(1n);
  }

  add(o: Rat): Rat {
    return Rat.of(this.n * o.d + o.n * this.d, this.d * o.d);
  }
  sub(o: Rat): Rat {
    return Rat.of(this.n * o.d - o.n * this.d, this.d * o.d);
  }
  mul(o: Rat): Rat {
    return Rat.of(this.n * o.n, this.d * o.d);
  }
  div(o: Rat): Rat {
    if (o.n === 0n) throw new Error('Rat: 除以零');
    return Rat.of(this.n * o.d, this.d * o.n);
  }
  neg(): Rat {
    return Rat.of(-this.n, this.d);
  }
  abs(): Rat {
    return Rat.of(abs(this.n), this.d);
  }
  cmp(o: Rat): number {
    const l = this.n * o.d;
    const r = o.n * this.d;
    return l < r ? -1 : l > r ? 1 : 0;
  }
  eq(o: Rat): boolean {
    return this.n === o.n && this.d === o.d;
  }
  isZero(): boolean {
    return this.n === 0n;
  }
  sign(): -1 | 0 | 1 {
    return this.n < 0n ? -1 : this.n > 0n ? 1 : 0;
  }

  /** 规范分式形式，如 "3/4"、"-7"、"1/8"。 */
  toString(): string {
    return this.d === 1n ? `${this.n}` : `${this.n}/${this.d}`;
  }

  /** 若为有限小数则返回精确十进制串，否则返回 null（不近似）。 */
  toDecimalExact(): string | null {
    let d = this.d;
    let c2 = 0;
    let c5 = 0;
    while (d % 2n === 0n) {
      d /= 2n;
      c2++;
    }
    while (d % 5n === 0n) {
      d /= 5n;
      c5++;
    }
    if (d !== 1n) return null;
    const places = Math.max(c2, c5);
    const scaled = this.n * 2n ** BigInt(places - c2) * 5n ** BigInt(places - c5);
    const neg = scaled < 0n;
    const digits = abs(scaled).toString().padStart(places + 1, '0');
    if (places === 0) return `${neg ? '-' : ''}${digits}`;
    const ip = digits.slice(0, -places);
    const fp = digits.slice(-places);
    return `${neg ? '-' : ''}${ip}.${fp}`;
  }

  /** 展示用：优先精确十进制，否则分式。 */
  display(): string {
    return this.toDecimalExact() ?? this.toString();
  }
}

/**
 * 显式舍入：把 x 舍入到 scale 位小数（10^-scale 的整数倍）。
 * 规则完全确定，逐条可复算。
 */
export function roundRat(x: Rat, scale: number, rule: RoundRule): Rat {
  if (!Number.isSafeInteger(scale) || scale < 0) {
    throw new Error(`非法的舍入标度: ${scale}`);
  }
  const p = 10n ** BigInt(scale);
  const num = x.n * p;
  const den = x.d; // den > 0
  let q = num / den; // BigInt 除法：向零截断
  const r = num % den; // 余数符号与 num 一致
  if (r !== 0n) {
    const s = num > 0n ? 1n : -1n; // 真值符号
    const twice = abs(r) * 2n;
    switch (rule) {
      case 'down':
        break; // 已截断
      case 'up':
        q += s;
        break;
      case 'floor':
        if (s < 0n) q -= 1n;
        break;
      case 'ceil':
        if (s > 0n) q += 1n;
        break;
      case 'half-up': // 半数远离零
        if (twice >= den) q += s;
        break;
      case 'half-down': // 半数朝向零
        if (twice > den) q += s;
        break;
      case 'half-even': // 半数向偶
        if (twice > den || (twice === den && q % 2n !== 0n)) q += s;
        break;
      default: {
        const neverRule: never = rule;
        throw new Error(`未知舍入规则: ${String(neverRule)}`);
      }
    }
  }
  return Rat.of(q, p);
}
