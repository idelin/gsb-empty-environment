/**
 * 内存跳表（SkipList）并发有序 MemTable。
 *
 * 设计要点：
 * - 多层随机链表：期望高度 O(log n)，查找 / 插入期望 O(log n)。
 * - 无锁指针语义：所有层间指针封装在 AtomicRef 中，插入通过
 *   compareExchange（CAS）逐层拼接，失败时重试，模拟锁自由（lock-free）
 *   的并发插入算法。JavaScript 单线程模型下同步代码段天然原子，
 *   因此 CAS 在运行时可保证不会失败，但算法结构与多线程无锁实现一致。
 * - MVCC 快照：每个键值节点携带单调递增的全局序列号 seq，同一键的
 *   多次写入以版本链（versions）形式保存。迭代器创建时记录快照序列号，
 *   遍历期间只读取 seq <= snapshotSeq 的版本，从而对后续并发写入
 *   （包括新键插入与旧键更新）完全隔离。
 */

const MAX_LEVEL = 16;
const PROBABILITY = 0.5;

/** 模拟原子引用：load / store / compareExchange（CAS）。 */
class AtomicRef<T> {
  private current: T;

  constructor(value: T) {
    this.current = value;
  }

  load(): T {
    return this.current;
  }

  store(value: T): void {
    this.current = value;
  }

  /** CAS：仅当当前值 === expected 时替换为 next，返回替换前的值。 */
  compareExchange(expected: T, next: T): T {
    const observed = this.current;
    if (observed === expected) {
      this.current = next;
    }
    return observed;
  }
}

interface VersionedValue<V> {
  readonly seq: number;
  readonly value: V;
}

class SkipNode<K, V> {
  readonly key: K;
  /** 节点首次插入时的全局序列号（即版本链中最老的 seq）。 */
  readonly seq: number;
  /** 版本链，按 seq 从新到旧排列（unshift 到头部）。 */
  readonly versions: VersionedValue<V>[];
  /** 每层的后继指针（原子引用）。 */
  readonly next: AtomicRef<SkipNode<K, V> | null>[];

  constructor(key: K, seq: number, value: V, level: number) {
    this.key = key;
    this.seq = seq;
    this.versions = [{ seq, value }];
    this.next = Array.from({ length: level }, () => new AtomicRef<SkipNode<K, V> | null>(null));
  }

  /** 读取对 snapshotSeq 可见的最新版本；不存在（节点晚于快照）时返回 undefined。 */
  visibleValue(snapshotSeq: number): V | undefined {
    for (const version of this.versions) {
      if (version.seq <= snapshotSeq) {
        return version.value;
      }
    }
    return undefined;
  }
}

export type CompareFn<K> = (a: K, b: K) => number;

function defaultCompare<K>(a: K, b: K): number {
  if (a === b) return 0;
  return (a as unknown) < (b as unknown) ? -1 : 1;
}

export interface RangeOptions<K> {
  /** 下界（缺省表示无下界）。 */
  start?: K;
  /** 上界（缺省表示无上界）。 */
  end?: K;
  /** 下界是否闭区间，默认 true。 */
  startInclusive?: boolean;
  /** 上界是否闭区间，默认 false。 */
  endInclusive?: boolean;
}

export class SkipList<K, V> {
  /** @internal 头哨兵节点，仅供迭代器内部访问。 */
  readonly head: SkipNode<K, V>;
  /** @internal 比较函数，仅供迭代器内部访问。 */
  readonly compare: CompareFn<K>;
  /** @internal 当前跳表高度。 */
  private level = 1;
  /** @internal 全局单调递增序列号。 */
  private seq = 0;
  private entryCount = 0;

  constructor(compare?: CompareFn<K>) {
    this.compare = compare ?? defaultCompare;
    this.head = new SkipNode<K, V>(undefined as unknown as K, 0, undefined as unknown as V, MAX_LEVEL);
  }

  get size(): number {
    return this.entryCount;
  }

  /** 当前全局序列号（即最新一次写入的版本号）。 */
  get currentSeq(): number {
    return this.seq;
  }

  private randomLevel(): number {
    let lvl = 1;
    while (lvl < MAX_LEVEL && Math.random() < PROBABILITY) {
      lvl += 1;
    }
    return lvl;
  }

  /**
   * 自顶向下查找 key，记录每一层的前驱与后继。
   * 返回值：若精确命中则返回该节点，否则返回 null。
   */
  private findPredecessors(
    key: K,
    preds: SkipNode<K, V>[],
    succs: (SkipNode<K, V> | null)[],
  ): SkipNode<K, V> | null {
    let current: SkipNode<K, V> = this.head;
    for (let lvl = this.level - 1; lvl >= 0; lvl -= 1) {
      let next = current.next[lvl].load();
      while (next !== null && this.compare(next.key, key) < 0) {
        current = next;
        next = current.next[lvl].load();
      }
      preds[lvl] = current;
      succs[lvl] = next;
    }
    const found = succs[0];
    return found !== null && this.compare(found.key, key) === 0 ? found : null;
  }

  /**
   * 插入或更新键值对。
   * - 键不存在：以 CAS 逐层拼接新节点（无锁插入）。
   * - 键已存在：向版本链追加一个新版本（保留旧版本供快照读取）。
   */
  insert(key: K, value: V): void {
    const preds: SkipNode<K, V>[] = new Array(MAX_LEVEL);
    const succs: (SkipNode<K, V> | null)[] = new Array(MAX_LEVEL);

    for (;;) {
      const found = this.findPredecessors(key, preds, succs);
      const seq = ++this.seq;

      if (found !== null) {
        // MVCC 更新：旧版本保留，快照迭代器仍可读旧值。
        found.versions.unshift({ seq, value });
        return;
      }

      const nodeLevel = this.randomLevel();
      const node = new SkipNode<K, V>(key, seq, value, nodeLevel);

      // 若新节点高度超过当前跳表高度，高出部分的前驱为头节点。
      for (let lvl = this.level; lvl < nodeLevel; lvl += 1) {
        preds[lvl] = this.head;
        succs[lvl] = this.head.next[lvl].load();
      }

      // 先挂好新节点的后继指针，再自底向上 CAS 拼接。
      for (let lvl = 0; lvl < nodeLevel; lvl += 1) {
        node.next[lvl].store(succs[lvl]);
      }

      let linked = true;
      for (let lvl = 0; lvl < nodeLevel; lvl += 1) {
        const observed = preds[lvl].next[lvl].compareExchange(succs[lvl], node);
        if (observed !== succs[lvl]) {
          // CAS 失败：并发插入改变了前驱的后继，重新定位后重试。
          linked = false;
          break;
        }
      }

      if (linked) {
        if (nodeLevel > this.level) {
          this.level = nodeLevel;
        }
        this.entryCount += 1;
        return;
      }
      // 重试前回滚序列号，保持 seq 与实际写入一一对应。
      this.seq -= 1;
    }
  }

  /** 点查：返回最新版本的值。 */
  get(key: K): V | undefined {
    let current: SkipNode<K, V> = this.head;
    for (let lvl = this.level - 1; lvl >= 0; lvl -= 1) {
      let next = current.next[lvl].load();
      while (next !== null && this.compare(next.key, key) < 0) {
        current = next;
        next = current.next[lvl].load();
      }
    }
    const node = current.next[0].load();
    if (node !== null && this.compare(node.key, key) === 0) {
      return node.versions[0].value;
    }
    return undefined;
  }

  /** @internal 定位第一个满足 compare(key, target) >= 0 的节点。 */
  seekFirst(target: K): SkipNode<K, V> | null {
    let current: SkipNode<K, V> = this.head;
    for (let lvl = this.level - 1; lvl >= 0; lvl -= 1) {
      let next = current.next[lvl].load();
      while (next !== null && this.compare(next.key, target) < 0) {
        current = next;
        next = current.next[lvl].load();
      }
    }
    return current.next[0].load();
  }

  /**
   * 创建只读快照迭代器。
   * 快照序列号在调用本方法的瞬间确定；此后对跳表的任何写入
   * （新键插入、旧键更新）都不会影响该迭代器的输出。
   */
  iterator(options: RangeOptions<K> = {}): SnapshotIterator<K, V> {
    return new SnapshotIterator<K, V>(this, options);
  }

  /** 范围扫描的便捷别名。 */
  range(options: RangeOptions<K> = {}): SnapshotIterator<K, V> {
    return this.iterator(options);
  }
}

/**
 * 只读快照迭代器：实现 IterableIterator<[K, V]>。
 * 遍历过程仅沿第 0 层后继指针前进，跳过晚于快照序列号的节点与版本。
 */
export class SnapshotIterator<K, V> implements IterableIterator<[K, V]> {
  private readonly list: SkipList<K, V>;
  private readonly snapshotSeq: number;
  private readonly end?: K;
  private readonly endInclusive: boolean;
  private node: SkipNode<K, V> | null;
  private finished = false;

  /** @internal 请通过 SkipList.iterator() / SkipList.range() 构造。 */
  constructor(list: SkipList<K, V>, options: RangeOptions<K>) {
    this.list = list;
    // 快照点：之后的写入对本迭代器不可见。
    this.snapshotSeq = list.currentSeq;
    this.end = options.end;
    this.endInclusive = options.endInclusive ?? false;

    const startInclusive = options.startInclusive ?? true;
    let node: SkipNode<K, V> | null;
    if (options.start === undefined) {
      node = list.head.next[0].load();
    } else {
      node = list.seekFirst(options.start);
      if (!startInclusive && node !== null && list.compare(node.key, options.start) === 0) {
        node = node.next[0].load();
      }
    }
    this.node = node;
  }

  /** 快照序列号，便于测试与诊断。 */
  get snapshot(): number {
    return this.snapshotSeq;
  }

  private withinUpperBound(key: K): boolean {
    if (this.end === undefined) return true;
    const cmp = this.list.compare(key, this.end);
    return this.endInclusive ? cmp <= 0 : cmp < 0;
  }

  next(): IteratorResult<[K, V]> {
    while (!this.finished && this.node !== null) {
      const current: SkipNode<K, V> = this.node;
      if (!this.withinUpperBound(current.key)) {
        this.finished = true;
        this.node = null;
        break;
      }
      // 沿第 0 层前进；新插入的节点若晚于快照会被下面的 seq 检查跳过。
      this.node = current.next[0].load();

      if (current.seq > this.snapshotSeq) {
        continue; // 节点整体晚于快照，不可见。
      }
      const value = current.visibleValue(this.snapshotSeq);
      if (value === undefined) {
        continue;
      }
      return { value: [current.key, value] as [K, V], done: false };
    }
    return { value: undefined, done: true };
  }

  [Symbol.iterator](): IterableIterator<[K, V]> {
    return this;
  }

  /** 收集剩余元素为数组。 */
  toArray(): [K, V][] {
    const out: [K, V][] = [];
    for (const entry of this) {
      out.push(entry);
    }
    return out;
  }
}
