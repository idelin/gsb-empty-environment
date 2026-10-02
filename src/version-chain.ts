/**
 * MVCC 多版本链管理与垃圾回收器。
 *
 * - 每个数据项维护一条按更新时间倒序（新->旧）链接的版本链表。
 * - 读操作携带 ReadView 时间戳，沿链表找到第一个 ts <= readTs 的版本。
 * - 系统追踪全局最小活跃 ReadView（GC 水位线），安全修剪被覆盖且
 *   对所有活跃/未来读者均不可见的废弃版本。
 *
 * 安全性假设：读时间戳来源于单调递增的全局时钟，因此未来读者的
 * readTs 不会小于当前的最小活跃 ReadView。
 */

/** 单条版本节点。next 指向更旧的版本。 */
export interface Version<T> {
  readonly value: T;
  readonly ts: number;
  next: Version<T> | null;
}

/** 活跃只读事务的 ReadView 句柄。 */
export interface ReadView {
  readonly id: number;
  readonly readTs: number;
  readonly active: boolean;
}

interface MutableReadView {
  id: number;
  readTs: number;
  active: boolean;
}

export class MvccStore<T> {
  private readonly chains = new Map<string, Version<T>>();
  private readonly activeViews = new Map<number, MutableReadView>();
  private nextViewId = 1;
  private prunedTotal = 0;

  /** 写入新版本，链表保持按 ts 倒序（新版本通常追加在头部）。 */
  put(key: string, value: T, ts: number): void {
    const node: Version<T> = { value, ts, next: null };
    const head = this.chains.get(key);
    if (head === undefined || ts > head.ts) {
      node.next = head ?? null;
      this.chains.set(key, node);
      return;
    }
    // 乱序写入：插入到正确位置，维持倒序。
    let prev: Version<T> | null = null;
    let curr: Version<T> | null = head;
    while (curr !== null && curr.ts > ts) {
      prev = curr;
      curr = curr.next;
    }
    if (curr !== null && curr.ts === ts) {
      throw new Error(`duplicate version timestamp ${ts} for key "${key}"`);
    }
    node.next = curr;
    if (prev !== null) {
      prev.next = node;
    }
  }

  /** 沿版本链查找对 readTs 可见的版本（第一个 ts <= readTs 的版本）。 */
  read(key: string, readTs: number): T | undefined {
    let curr = this.chains.get(key) ?? null;
    while (curr !== null) {
      if (curr.ts <= readTs) {
        return curr.value;
      }
      curr = curr.next;
    }
    return undefined;
  }

  /** 开启一个只读事务，登记其 ReadView。 */
  beginRead(readTs: number): ReadView {
    const view: MutableReadView = {
      id: this.nextViewId++,
      readTs,
      active: true,
    };
    this.activeViews.set(view.id, view);
    return view;
  }

  /** 结束只读事务，释放其 ReadView。 */
  endRead(view: ReadView): void {
    const held = this.activeViews.get(view.id);
    if (held !== undefined) {
      held.active = false;
      this.activeViews.delete(view.id);
    }
  }

  /** 全局最小活跃 ReadView 时间戳；无活跃读者时为 Infinity。 */
  get minActiveReadTs(): number {
    let min = Infinity;
    for (const view of this.activeViews.values()) {
      if (view.readTs < min) {
        min = view.readTs;
      }
    }
    return min;
  }

  /**
   * 垃圾回收：对每条版本链，找到不晚于水位线的最新版本 v
   * （即所有活跃读者可见集合中最老但仍可见的版本），截断并释放
   * v 之后的全部更旧版本。返回本次修剪掉的版本数。
   */
  gc(): number {
    const watermark = this.minActiveReadTs;
    let pruned = 0;
    for (const head of this.chains.values()) {
      let curr: Version<T> | null = head;
      while (curr !== null && curr.ts > watermark) {
        curr = curr.next;
      }
      if (curr !== null && curr.next !== null) {
        let dropped = 0;
        let tail: Version<T> | null = curr.next;
        while (tail !== null) {
          dropped++;
          tail = tail.next;
        }
        curr.next = null; // 断开引用，废弃版本可被 GC 释放
        pruned += dropped;
      }
    }
    this.prunedTotal += pruned;
    return pruned;
  }

  /** 累计被修剪（释放）的版本数。 */
  get prunedCount(): number {
    return this.prunedTotal;
  }

  /** 某数据项当前版本链长度（用于观测与测试）。 */
  chainLength(key: string): number {
    let length = 0;
    let curr = this.chains.get(key) ?? null;
    while (curr !== null) {
      length++;
      curr = curr.next;
    }
    return length;
  }

  /** 某数据项版本链上的时间戳序列（新->旧），用于观测与测试。 */
  chainTimestamps(key: string): number[] {
    const result: number[] = [];
    let curr = this.chains.get(key) ?? null;
    while (curr !== null) {
      result.push(curr.ts);
      curr = curr.next;
    }
    return result;
  }
}
