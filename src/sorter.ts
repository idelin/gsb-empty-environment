import {
  Interval,
  disjointOrder,
  formatInterval,
  intervalContains,
  isTighteningOf,
  isValidInterval,
  width,
} from './interval';
import { IntervalProvider, ProviderContractError } from './provider';
import { Rational } from './rational';

export interface TopKOptions {
  /** 需要确定的前 K 名（按值降序）。 */
  k: number;
  /** refine 调用总预算（每次 refine 花费 1）。 */
  budget: number;
  /** 每次成功 refine 后回调，可用于中途 cancel。 */
  onStep?: (info: { used: number; budget: number }) => void;
}

export type TopKStatus =
  | 'complete' // 前 K 已完全确定
  | 'budget-exhausted' // 预算耗尽，返回部分结果
  | 'providers-exhausted' // 提供者均已无能为力，仍有未决
  | 'cancelled'; // 被取消

export interface ItemSnapshot {
  readonly id: string;
  readonly lo: Rational;
  readonly hi: Rational;
  readonly exact: boolean;
  readonly exhausted: boolean;
}

export interface TopKResult {
  readonly status: TopKStatus;
  /** 已确定的前缀：有序的分组，组内为经精确证书证明相等的并列项。 */
  readonly prefix: ReadonlyArray<ReadonlyArray<string>>;
  /** 未决项：按"互相无法判定"关系连通分组，组间顺序同样未决。 */
  readonly pending: ReadonlyArray<ReadonlyArray<string>>;
  readonly budgetUsed: number;
  readonly items: ReadonlyArray<ItemSnapshot>;
}

interface ItemState {
  readonly id: string;
  readonly provider: IntervalProvider;
  lo: Rational;
  hi: Rational;
  exact: boolean;
  exhausted: boolean;
}

/**
 * 前 K 排序器。
 *
 * 判定规则（保守，绝不猜测）：
 * - a 严格排在 b 前 ⟺ 区间严格不相交：a.lo > b.hi；
 * - a 与 b 并列 ⟺ 双方都给出精确证书且值相等；
 * - 其余一律视为未决（包括端点相接、区间重叠、一方未给证书）。
 * 区间中点/宽度只用于调度选择，绝不参与排序判定。
 */
export class TopKSorter {
  private readonly states: ItemState[];
  private readonly k: number;
  private readonly budget: number;
  private readonly onStep?: TopKOptions['onStep'];
  private used = 0;
  private cancelled = false;

  constructor(providers: ReadonlyArray<IntervalProvider>, options: TopKOptions) {
    if (options.k < 0) throw new Error('k must be >= 0');
    if (options.budget < 0) throw new Error('budget must be >= 0');
    this.k = options.k;
    this.budget = options.budget;
    this.onStep = options.onStep;
    this.states = providers.map((provider) => {
      const first = provider.refine();
      if (first.kind !== 'interval') {
        throw new ProviderContractError(
          provider.id,
          'invalid-interval',
          'first refine() must yield the initial interval',
        );
      }
      if (!isValidInterval(first.interval)) {
        throw new ProviderContractError(provider.id, 'invalid-interval', 'lo > hi');
      }
      return {
        id: provider.id,
        provider,
        lo: first.interval.lo,
        hi: first.interval.hi,
        exact: false,
        exhausted: false,
      };
    });
  }

  cancel(): void {
    if (this.cancelled) return;
    this.cancelled = true;
    for (const s of this.states) s.provider.cancel();
  }

  run(): TopKResult {
    let status: TopKStatus | null = null;
    while (status === null) {
      const { prefix, remaining } = this.extractPrefix();
      if (this.countItems(prefix) >= this.k || remaining.length === 0) {
        status = 'complete';
        break;
      }
      if (this.cancelled) {
        status = 'cancelled';
        break;
      }
      if (this.used >= this.budget) {
        status = 'budget-exhausted';
        break;
      }
      const candidate = this.pickCandidate(remaining);
      if (candidate === null) {
        status = 'providers-exhausted';
        break;
      }
      this.refineOnce(candidate);
    }
    return this.buildResult(status);
  }

  /** 已证明：a 严格大于 b（有效区间严格不相交；精确值已折叠进区间）。 */
  private provenGt(a: ItemState, b: ItemState): boolean {
    return disjointOrder({ lo: a.lo, hi: a.hi }, { lo: b.lo, hi: b.hi }) === 1;
  }

  /** 已证明：a 与 b 相等（双方精确证书且值相等）。 */
  private provenEq(a: ItemState, b: ItemState): boolean {
    return a.exact && b.exact && a.lo.eq(b.lo);
  }

  /**
   * 从剩余项中反复提取"被证明不劣于所有剩余项"的最大组，
   * 得到确定前缀；剩下的即未决集合。
   */
  private extractPrefix(): { prefix: string[][]; remaining: ItemState[] } {
    const remaining = [...this.states];
    const prefix: string[][] = [];
    for (;;) {
      const tops = remaining.filter((m) =>
        remaining.every((o) => m === o || this.provenGt(m, o) || this.provenEq(m, o)),
      );
      if (tops.length === 0) break;
      prefix.push(tops.map((t) => t.id));
      const topSet = new Set(tops);
      for (let i = remaining.length - 1; i >= 0; i--) {
        if (topSet.has(remaining[i])) remaining.splice(i, 1);
      }
    }
    return { prefix, remaining };
  }

  /**
   * 调度：在未决集合中挑"仍存在未证明关系"且仍可 refine 的项，
   * 选区间最宽者（确定性：宽度相同按 id 字典序）。
   */
  private pickCandidate(remaining: ItemState[]): ItemState | null {
    const ambiguous = remaining.filter(
      (s) =>
        !s.exact &&
        !s.exhausted &&
        remaining.some((o) => o !== s && !this.provenGt(s, o) && !this.provenGt(o, s) && !this.provenEq(s, o)),
    );
    if (ambiguous.length === 0) return null;
    ambiguous.sort((a, b) => {
      const byWidth = width({ lo: b.lo, hi: b.hi }).cmp(width({ lo: a.lo, hi: a.hi }));
      return byWidth !== 0 ? byWidth : a.id < b.id ? -1 : 1;
    });
    return ambiguous[0];
  }

  private refineOnce(s: ItemState): void {
    if (s.exact) {
      throw new ProviderContractError(s.id, 'refine-after-exact', 'already certified exact');
    }
    if (s.exhausted) {
      throw new ProviderContractError(s.id, 'refine-after-exhausted', 'already exhausted');
    }
    const prev: Interval = { lo: s.lo, hi: s.hi };
    const r = s.provider.refine();
    this.used += 1;
    switch (r.kind) {
      case 'interval': {
        if (!isValidInterval(r.interval)) {
          throw new ProviderContractError(s.id, 'invalid-interval', 'lo > hi');
        }
        if (!isTighteningOf(prev, r.interval)) {
          throw new ProviderContractError(
            s.id,
            'not-tightening',
            `${formatInterval(r.interval)} is not a subset of ${formatInterval(prev)}`,
          );
        }
        s.lo = r.interval.lo;
        s.hi = r.interval.hi;
        break;
      }
      case 'exact': {
        if (!intervalContains(prev, r.value)) {
          throw new ProviderContractError(
            s.id,
            'exact-outside-interval',
            `${r.value.toString()} not in ${formatInterval(prev)}`,
          );
        }
        s.lo = r.value;
        s.hi = r.value;
        s.exact = true;
        break;
      }
      case 'exhausted': {
        s.exhausted = true;
        break;
      }
    }
    this.onStep?.({ used: this.used, budget: this.budget });
  }

  private buildResult(status: TopKStatus): TopKResult {
    const { prefix, remaining } = this.extractPrefix();
    return {
      status,
      prefix,
      pending: this.groupPending(remaining),
      budgetUsed: this.used,
      items: this.states.map((s) => ({
        id: s.id,
        lo: s.lo,
        hi: s.hi,
        exact: s.exact,
        exhausted: s.exhausted,
      })),
    };
  }

  /** 未决项按"互相无法判定顺序"关系的连通分量分组。 */
  private groupPending(remaining: ItemState[]): string[][] {
    const parent = new Map<string, string>();
    const find = (x: string): string => {
      let root = x;
      while (parent.get(root) !== root) root = parent.get(root)!;
      let cur = x;
      while (parent.get(cur) !== cur) {
        const next = parent.get(cur)!;
        parent.set(cur, root);
        cur = next;
      }
      return root;
    };
    for (const s of remaining) parent.set(s.id, s.id);
    for (let i = 0; i < remaining.length; i++) {
      for (let j = i + 1; j < remaining.length; j++) {
        const a = remaining[i];
        const b = remaining[j];
        if (!this.provenGt(a, b) && !this.provenGt(b, a)) {
          parent.set(find(a.id), find(b.id));
        }
      }
    }
    const groups = new Map<string, string[]>();
    for (const s of remaining) {
      const root = find(s.id);
      const g = groups.get(root);
      if (g) g.push(s.id);
      else groups.set(root, [s.id]);
    }
    return [...groups.values()];
  }

  private countItems(prefix: ReadonlyArray<ReadonlyArray<string>>): number {
    return prefix.reduce((n, g) => n + g.length, 0);
  }
}
