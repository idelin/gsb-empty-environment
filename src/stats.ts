import {
  add, sub, mul, divBig, makeRational, parseDecimal, ZERO,
} from './rational.ts';
import type { Rational } from './rational.ts';

export type VarianceKind = 'sample' | 'population';

export interface StatsSnapshot {
  readonly count: number;
  readonly sum: Rational;
  readonly mean: Rational | null;
  readonly sampleVariance: Rational | null;
  readonly populationVariance: Rational | null;
}

type Op =
  | { kind: 'add'; id: number; value: Rational; evicted?: { id: number; value: Rational } }
  | { kind: 'remove'; id: number; value: Rational; index: number };

/**
 * 滑窗精确统计：sum / mean / variance。
 *
 * - 全部状态为 BigInt 有理数：增量维护 Σx 与 Σx²，增删互为逆运算，绝不经过 Number。
 * - 方差用精确公式 (n·Σx² − (Σx)²) / denom 计算，无浮点，因此不存在灾难消减；
 *   大偏移 + 小方差（如 1e30 附近相差 1）依然精确。
 * - 样本方差分母 n(n−1)，n < 2 时返回 null（未定义）；总体方差分母 n²，n = 0 时返回 null。
 * - undo() 按操作日志逆序回滚（含 maxSize 自动驱逐），精确恢复原统计。
 */
export class ExactWindowStats {
  readonly maxSize?: number;
  private values = new Map<number, Rational>();
  private order: number[] = [];
  private ops: Op[] = [];
  private nextId = 1;
  private sumR: Rational = ZERO;
  private sumSqR: Rational = ZERO;

  constructor(opts: { maxSize?: number } = {}) {
    if (opts.maxSize !== undefined) {
      if (!Number.isInteger(opts.maxSize) || opts.maxSize < 1) {
        throw new Error('maxSize must be a positive integer');
      }
      this.maxSize = opts.maxSize;
    }
  }

  /** 加入一个值（十进制字符串或 bigint），返回用于 remove 的 id。 */
  add(input: string | bigint): number {
    const value = parseDecimal(input);
    const id = this.nextId++;
    let evicted: { id: number; value: Rational } | undefined;
    if (this.maxSize !== undefined && this.order.length >= this.maxSize) {
      const evictId = this.order[0];
      evicted = { id: evictId, value: this.values.get(evictId)! };
      this.applyRemove(evictId);
    }
    this.applyAdd(id, value);
    this.ops.push({ kind: 'add', id, value, evicted });
    return id;
  }

  /** 按 id 删除一个值（重复值各自持有独立 id，互不影响）。 */
  remove(id: number): void {
    const value = this.values.get(id);
    if (value === undefined) throw new Error(`unknown id: ${id}`);
    const index = this.order.indexOf(id);
    this.applyRemove(id);
    this.ops.push({ kind: 'remove', id, value, index });
  }

  /** 撤销最近一次 add/remove（含其引发的驱逐），恢复之前的确切统计。 */
  undo(): boolean {
    const op = this.ops.pop();
    if (op === undefined) return false;
    if (op.kind === 'add') {
      this.applyRemove(op.id);
      if (op.evicted !== undefined) this.applyAdd(op.evicted.id, op.evicted.value, 0);
    } else {
      this.applyAdd(op.id, op.value, op.index);
    }
    return true;
  }

  get count(): number {
    return this.values.size;
  }

  has(id: number): boolean {
    return this.values.has(id);
  }

  /** 窗口内精确和；空窗口为 0/1。 */
  sum(): Rational {
    return this.sumR;
  }

  /** 精确均值；空窗口返回 null。 */
  mean(): Rational | null {
    if (this.values.size === 0) return null;
    return divBig(this.sumR, BigInt(this.values.size));
  }

  /**
   * 精确方差。
   * kind='sample'（默认）：分母 n−1，n < 2 时返回 null（未定义）。
   * kind='population'：分母 n，n = 0 时返回 null。
   */
  variance(kind: VarianceKind = 'sample'): Rational | null {
    const n = BigInt(this.values.size);
    if (n === 0n) return null;
    if (kind === 'sample' && n < 2n) return null;
    // t = n·Σx² − (Σx)²，全程 BigInt 精确，数学上保证非负。
    // Σ(x−mean)² = t/n，故样本方差 = t/(n(n−1))，总体方差 = t/n²。
    const t = sub(mul({ num: n, den: 1n }, this.sumSqR), mul(this.sumR, this.sumR));
    const denom = kind === 'sample' ? n * (n - 1n) : n * n;
    return makeRational(t.num, t.den * denom);
  }

  /** 当前统计快照（有理数引用不可变，可安全保存用于对比）。 */
  snapshot(): StatsSnapshot {
    return {
      count: this.count,
      sum: this.sum(),
      mean: this.mean(),
      sampleVariance: this.variance('sample'),
      populationVariance: this.variance('population'),
    };
  }

  private applyAdd(id: number, value: Rational, index?: number): void {
    this.values.set(id, value);
    if (index === undefined) this.order.push(id);
    else this.order.splice(Math.min(index, this.order.length), 0, id);
    this.sumR = add(this.sumR, value);
    this.sumSqR = add(this.sumSqR, mul(value, value));
  }

  private applyRemove(id: number): void {
    const value = this.values.get(id)!;
    this.values.delete(id);
    this.order.splice(this.order.indexOf(id), 1);
    this.sumR = sub(this.sumR, value);
    this.sumSqR = sub(this.sumSqR, mul(value, value));
  }
}
