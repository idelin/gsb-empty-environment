/**
 * 滑动窗口精确统计：sum / mean / variance（样本与总体）。
 *
 * 设计要点：
 * - 内部只维护精确量：计数 n、Σx、Σx²（均为 BigInt 有理数），
 *   方差 = (Σx² − (Σx)²/n) / (n−1|n) 全程精确计算，
 *   不存在浮点意义上的灾难消减——大偏移 + 小方差场景依然精确。
 * - 用多重集合（值 → 出现次数）支持重复值的增删；
 *   删除即精确回滚 Σx / Σx²，统计量恢复到添加前的状态。
 */

import { isqrt, Rational } from './rational.ts';
import type { RationalInput, RoundingMode } from './rational.ts';

export type VarianceKind = 'sample' | 'population';

export interface StatsSnapshot {
  count: number;
  sum: string;
  mean: string | null;
  sampleVariance: string | null;
  populationVariance: string | null;
  sampleStddev: string | null;
  populationStddev: string | null;
}

export class WindowStats {
  #count = 0;
  #sum: Rational = Rational.zero;
  #sumSq: Rational = Rational.zero;
  readonly #counts = new Map<string, { value: Rational; refs: number }>();

  get count(): number {
    return this.#count;
  }

  get size(): number {
    return this.#count;
  }

  isEmpty(): boolean {
    return this.#count === 0;
  }

  /** 加入一个值（重复值按多重集合累计）。返回 this 便于链式调用。 */
  add(input: RationalInput): this {
    const value = Rational.from(input);
    this.#sum = this.#sum.add(value);
    this.#sumSq = this.#sumSq.add(value.mul(value));
    const key = value.toString();
    const entry = this.#counts.get(key);
    if (entry) entry.refs += 1;
    else this.#counts.set(key, { value, refs: 1 });
    this.#count += 1;
    return this;
  }

  /**
   * 移除一个值的一次出现，精确撤销其对 Σx / Σx² 的贡献。
   * 若该值不在窗口中，抛出 RangeError。
   */
  remove(input: RationalInput): this {
    const value = Rational.from(input);
    const key = value.toString();
    const entry = this.#counts.get(key);
    if (!entry) {
      throw new RangeError(`Cannot remove absent value: ${value.toString()}`);
    }
    this.#sum = this.#sum.sub(entry.value);
    this.#sumSq = this.#sumSq.sub(entry.value.mul(entry.value));
    entry.refs -= 1;
    if (entry.refs === 0) this.#counts.delete(key);
    this.#count -= 1;
    return this;
  }

  reset(): void {
    this.#count = 0;
    this.#sum = Rational.zero;
    this.#sumSq = Rational.zero;
    this.#counts.clear();
  }

  /** 精确和；空窗口即 0。 */
  sum(): Rational {
    return this.#sum;
  }

  /** 精确均值；空窗口抛 RangeError。 */
  mean(): Rational {
    if (this.#count === 0) throw new RangeError('mean: empty window');
    return this.#sum.div(BigInt(this.#count));
  }

  /**
   * 精确方差。
   * - sample：无偏样本方差，除以 n−1；n < 2 时未定义，返回 null。
   * - population：总体方差，除以 n；n < 1 时未定义，返回 null。
   */
  variance(kind: VarianceKind = 'sample'): Rational | null {
    if (kind === 'sample' && this.#count < 2) return null;
    if (kind === 'population' && this.#count < 1) return null;
    const n = BigInt(this.#count);
    // 二阶中心矩的分子：Σx² − (Σx)²/n，精确有理数，无消减误差。
    const moment2 = this.#sumSq.sub(this.#sum.mul(this.#sum).div(n));
    return moment2.div(kind === 'sample' ? n - 1n : n);
  }

  sampleVariance(): Rational | null {
    return this.variance('sample');
  }

  populationVariance(): Rational | null {
    return this.variance('population');
  }

  /**
   * 标准差的十进制字符串（按显式舍入规则）。
   * 方差是有理数，平方根一般无理；这里用整数平方根在 BigInt 上
   * 精确判定舍入方向，不经过浮点开方。未定义时返回 null。
   */
  standardDeviation(
    kind: VarianceKind = 'sample',
    places: number,
    mode: RoundingMode = 'half-even',
  ): string | null {
    const v = this.variance(kind);
    if (v === null) return null;
    return sqrtToDecimalString(v, places, mode);
  }

  /** 汇总快照：所有统计量按同一舍入规则格式化为字符串。 */
  snapshot(places: number, mode: RoundingMode = 'half-even'): StatsSnapshot {
    const fmt = (r: Rational | null): string | null =>
      r === null ? null : r.toDecimalString(places, mode);
    return {
      count: this.#count,
      sum: this.#sum.toDecimalString(places, mode),
      mean: this.#count === 0 ? null : fmt(this.mean()),
      sampleVariance: fmt(this.variance('sample')),
      populationVariance: fmt(this.variance('population')),
      sampleStddev: this.standardDeviation('sample', places, mode),
      populationStddev: this.standardDeviation('population', places, mode),
    };
  }
}

/** round(sqrt(v) * 10^places)，在 BigInt 上精确比较，按 mode 舍入。 */
function sqrtToDecimalString(
  v: Rational,
  places: number,
  mode: RoundingMode,
): string {
  const scale = 10n ** BigInt(places);
  const scaledNum = v.num * scale * scale; // v >= 0，num >= 0
  const den = v.den;
  // m = floor(sqrt(scaledNum / den))；m² <= scaledNum/den 当且仅当 m²*den <= scaledNum
  const m = isqrt(scaledNum / den);
  const lower = m * m * den;
  let q = m;
  if (lower !== scaledNum) {
    // 与 m + 1/2 比较：sqrt(N/D) ? m+1/2  <=>  4N ? D(2m+1)²…
    // 用 4N ? D(4m²+4m+1) 避免分数。
    const cmp = 4n * scaledNum - den * (4n * m * m + 4n * m + 1n);
    const up =
      mode === 'up' ||
      mode === 'ceil' || // v >= 0，ceil 等价于远离零
      (mode === 'half-up' && cmp >= 0n) ||
      (mode === 'half-even' && (cmp > 0n || (cmp === 0n && (m & 1n) === 1n))) ||
      (mode === 'half-down' && cmp > 0n);
    if (up) q = m + 1n;
  }
  let digits = q.toString();
  if (places > 0) {
    digits = digits.padStart(places + 1, '0');
    const split = digits.length - places;
    digits = `${digits.slice(0, split)}.${digits.slice(split)}`;
  }
  return digits;
}
