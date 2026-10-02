/**
 * MVCC 多版本链管理与垃圾回收器。
 *
 * - 每个数据项对应一条版本链，版本按更新时间倒序（新->旧）链接。
 * - 读操作携带 ReadView（读时间戳），沿链表找到第一个 t <= readView.t 的版本。
 * - ReadViewRegistry 追踪全局最小活跃 ReadView；GC 据此修剪
 *   “已被覆盖且对任何当前/未来读者都不可见”的废弃版本。
 */

/** 只读事务的可见性视图：t 为读时间戳（快照点）。 */
export interface ReadView {
  readonly id: number;
  readonly t: number;
}

/** 版本节点：单向链表，next 指向更旧的版本。 */
export class Version<T> {
  /** 全局存活版本计数，用于验证 GC 确实释放了内存。 */
  static allocated = 0;

  readonly t: number;
  readonly value: T;
  next: Version<T> | null = null;

  constructor(t: number, value: T) {
    this.t = t;
    this.value = value;
    Version.allocated++;
  }
}

/** 单 key 的版本链，head 为最新版本（按 t 倒序）。 */
export class VersionChain<T> {
  head: Version<T> | null = null;

  get size(): number {
    let n = 0;
    for (let v = this.head; v !== null; v = v.next) n++;
    return n;
  }

  /** 按更新时间倒序插入；同一 key 的时间戳必须唯一。 */
  put(t: number, value: T): Version<T> {
    const node = new Version(t, value);
    if (this.head === null || t > this.head.t) {
      node.next = this.head;
      this.head = node;
      return node;
    }
    let prev: Version<T> | null = null;
    let cur: Version<T> | null = this.head;
    while (cur !== null && cur.t > t) {
      prev = cur;
      cur = cur.next;
    }
    if (cur !== null && cur.t === t) {
      Version.allocated--; // 回滚计数，节点未入链
      throw new Error(`duplicate version timestamp t=${t}`);
    }
    node.next = cur;
    prev!.next = node;
    return node;
  }

  /** 沿链表查找对读时间戳 t 可见的版本（第一个 version.t <= t）。 */
  read(t: number): T | undefined {
    for (let v = this.head; v !== null; v = v.next) {
      if (v.t <= t) return v.value;
    }
    return undefined;
  }

  /**
   * 修剪对 minVisibleT（全局最小活跃 ReadView）不可见的废弃版本。
   *
   * 保留“对 minVisibleT 可见的最新版本”及所有比它新的版本；
   * 其余更旧版本已被覆盖，且任何当前/未来读者（t >= minVisibleT）
   * 都只会命中保留的那个版本，因此可以安全释放。
   *
   * @returns 被修剪掉的版本（按新->旧顺序）
   */
  prune(minVisibleT: number): Version<T>[] {
    let cur = this.head;
    while (cur !== null && cur.t > minVisibleT) cur = cur.next;
    // cur 为对 minVisibleT 可见的最新版本；cur 之后（更旧）的都是垃圾。
    if (cur === null) return []; // 所有版本都新于 minVisibleT，保守全部保留
    const pruned: Version<T>[] = [];
    let garbage = cur.next;
    cur.next = null;
    while (garbage !== null) {
      pruned.push(garbage);
      Version.allocated--;
      const next = garbage.next;
      garbage.next = null; // 断开引用，便于 GC 回收
      garbage = next;
    }
    return pruned;
  }

  /** 调试辅助：返回链上版本时间戳（新->旧）。 */
  timestamps(): number[] {
    const out: number[] = [];
    for (let v = this.head; v !== null; v = v.next) out.push(v.t);
    return out;
  }
}

/** 追踪全局活跃只读事务，计算最小活跃 ReadView。 */
export class ReadViewRegistry {
  private active = new Map<number, number>(); // readView id -> t
  private nextId = 1;

  /** 开启一个只读事务，返回其 ReadView。 */
  begin(t: number): ReadView {
    const id = this.nextId++;
    this.active.set(id, t);
    return { id, t };
  }

  /** 只读事务结束，释放其 ReadView。 */
  end(view: ReadView): void {
    if (!this.active.delete(view.id)) {
      throw new Error(`read view #${view.id} is not active`);
    }
  }

  /** 全局最小活跃 ReadView 的时间戳；无活跃读者时返回 null。 */
  get minActive(): number | null {
    let min: number | null = null;
    for (const t of this.active.values()) {
      if (min === null || t < min) min = t;
    }
    return min;
  }

  get activeCount(): number {
    return this.active.size;
  }
}

export interface GcReport {
  /** key -> 被修剪版本的时间戳（新->旧）。 */
  pruned: Map<string, number[]>;
  /** 本次释放的版本节点总数。 */
  freedVersions: number;
  /** 本次 GC 使用的最小可见时间戳（无活跃读者时为 Infinity）。 */
  minVisibleT: number;
}

/** MVCC 存储：管理多个数据项的版本链，并基于全局最小 ReadView 做 GC。 */
export class MvccStore {
  private chains = new Map<string, VersionChain<unknown>>();
  readonly registry = new ReadViewRegistry();

  private chainFor<T>(key: string): VersionChain<T> {
    let chain = this.chains.get(key);
    if (chain === undefined) {
      chain = new VersionChain<T>();
      this.chains.set(key, chain);
    }
    return chain as VersionChain<T>;
  }

  /** 写入 key 在时刻 t 的新版本。 */
  put<T>(key: string, t: number, value: T): void {
    this.chainFor<T>(key).put(t, value);
  }

  /** 以 ReadView（或其时间戳）读取 key 的可见版本。 */
  read<T>(key: string, view: ReadView | number): T | undefined {
    const t = typeof view === "number" ? view : view.t;
    const chain = this.chains.get(key);
    return chain === undefined ? undefined : (chain.read(t) as T | undefined);
  }

  /** 查看某 key 的版本链（测试/调试用）。 */
  chain(key: string): VersionChain<unknown> | undefined {
    return this.chains.get(key);
  }

  /**
   * 垃圾回收：修剪所有 key 上不再对任何活跃读者可见的废弃版本。
   * 无活跃读者时，minVisibleT 取 +Infinity，即每个 key 只保留最新版本。
   */
  gc(): GcReport {
    const minActive = this.registry.minActive;
    const minVisibleT = minActive === null ? Number.POSITIVE_INFINITY : minActive;
    const pruned = new Map<string, number[]>();
    let freedVersions = 0;
    for (const [key, chain] of this.chains) {
      const removed = chain.prune(minVisibleT);
      if (removed.length > 0) {
        pruned.set(key, removed.map((v) => v.t));
        freedVersions += removed.length;
      }
    }
    return { pruned, freedVersions, minVisibleT };
  }
}
