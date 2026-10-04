/**
 * 费率以有理数（分子/分母）表示，全程 BigInt 运算，杜绝浮点误差。
 * 输入接受十进制字符串或 number（number 会经 String() 转为十进制串再解析，
 * 例如 0.005 -> "0.005"，不会引入二进制浮点误差）。
 */
export interface RationalRate {
  readonly num: bigint;
  readonly den: bigint;
}

export type RateInput = string | number;

export function parseRate(rate: RateInput): RationalRate {
  const text = typeof rate === 'number' ? String(rate) : rate.trim();
  if (!/^-?\d+(\.\d+)?$/.test(text)) {
    throw new Error(`无效的费率: ${JSON.stringify(rate)}，应为十进制数，如 "0.005"`);
  }
  const negative = text.startsWith('-');
  const body = negative ? text.slice(1) : text;
  const dot = body.indexOf('.');
  const digits = dot === -1 ? body : body.slice(0, dot) + body.slice(dot + 1);
  const scale = dot === -1 ? 0 : body.length - dot - 1;
  let num = BigInt(digits === '' ? '0' : digits);
  let den = 10n ** BigInt(scale);
  if (negative) num = -num;
  if (num < 0n) {
    throw new Error(`费率不能为负: ${JSON.stringify(rate)}`);
  }
  // 约分，保持数值最小化
  const g = gcd(num, den);
  return { num: num / g, den: den / g };
}

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

/** 将有理数费率格式化为十进制字符串（去除多余尾零），仅用于输出展示。 */
export function formatRate(rate: RationalRate): string {
  if (rate.num === 0n) return '0';
  // 分母为 10^k 时可直接十进制展开；否则按 12 位小数截断展示
  let den = rate.den;
  let scale = 0;
  let d = den;
  while (d % 10n === 0n) {
    d /= 10n;
    scale += 1;
  }
  let twos = 0;
  let fives = 0;
  let t = d;
  while (t % 2n === 0n && t !== 0n) {
    t /= 2n;
    twos += 1;
  }
  while (t % 5n === 0n && t !== 0n) {
    t /= 5n;
    fives += 1;
  }
  if (t === 1n) {
    const extra = Math.max(twos, fives);
    const num = rate.num * 2n ** BigInt(extra - twos) * 5n ** BigInt(extra - fives);
    const digits = num.toString().padStart(scale + extra + 1, '0');
    const intPart = digits.slice(0, digits.length - scale - extra) || '0';
    let fracPart = digits.slice(digits.length - scale - extra);
    fracPart = fracPart.replace(/0+$/, '');
    return fracPart === '' ? intPart : `${intPart}.${fracPart}`;
  }
  // 非有限小数：展示 12 位小数
  const scaled = (rate.num * 10n ** 12n) / rate.den;
  const digits = scaled.toString().padStart(13, '0');
  const intPart = digits.slice(0, digits.length - 12);
  const fracPart = digits.slice(digits.length - 12).replace(/0+$/, '');
  return fracPart === '' ? intPart : `${intPart}.${fracPart}`;
}
