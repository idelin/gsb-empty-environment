/**
 * 分层时间感知（TTL/TTD）LRU 双重索引器。
 *
 * 双重索引结构：
 *  1. 访问热度层：HashMap + 双向链表（MRU -> LRU），get/set 单次操作内 O(1) 热度提升。
 *  2. 时间层：按到期时间排序的小顶堆，后台清扫器批量弹出已过期死对象。
 *
 * 时间语义：
 *  - ttlMs     绝对过期时间（自写入起算，硬上限，不可被访问续期）。
 *  - slidingMs 滑动过期时间（TTD，每次访问自该时刻续期，但不得突破绝对上限）。
 *  - expiresAt = min(createdAt + ttlMs, lastTouchAt + slidingMs)。
 */
export interface SetOptions {
  /** 绝对过期时间（毫秒）。Infinity 表示永不过期。 */
  ttlMs?: number;
  /** 滑动过期时间（毫秒），每次访问自动续期。Infinity 表示不启用。 */
  slidingMs?: number;
}

export interface CacheOptions {
  /** 最大容量，超出时淘汰链表尾部的最冷对象。默认 Infinity。 */
  maxSize?: number;
  /** 后台批量清扫周期（毫秒）。<= 0 表示关闭后台清扫。默认 1000。 */
  sweepIntervalMs?: number;
  /** 时钟注入点，便于测试。默认 Date.now。 */
  now?: () => number;
}

interface Entry<V> {
  key: string;
  value: V;
  prev: Entry<V> | null;
  next: Entry<V> | null;
  absoluteExpiry: number;
  slidingMs: number;
  expiresAt: number;
  heapIndex: number;
}

export class TtlLruCache<V> {
  private readonly maxSize: number;
  private readonly sweepIntervalMs: number;
  private readonly now: () => number;

  private readonly map = new Map<string, Entry<V>>();
  private head: Entry<V> | null = null; // 最热（MRU）
  private tail: Entry<V> | null = null; // 最冷（LRU）
  private readonly heap: Entry<V>[] = []; // 按 expiresAt 排序的小顶堆
  private sweeper: ReturnType<typeof setInterval> | null = null;

  constructor(options: CacheOptions = {}) {
    this.maxSize = options.maxSize ?? Infinity;
    this.sweepIntervalMs = options.sweepIntervalMs ?? 1000;
    this.now = options.now ?? Date.now;
    if (this.sweepIntervalMs > 0) {
      this.sweeper = setInterval(() => this.sweepExpired(), this.sweepIntervalMs);
      // 不阻止进程退出，避免句柄泄漏
      this.sweeper.unref?.();
    }
  }

  get size(): number {
    return this.map.size;
  }

  /** 时间层中待到期对象数量（用于泄漏自检）。 */
  get pendingExpirations(): number {
    return this.heap.length;
  }

  /** 后台清扫定时器是否存活（用于泄漏自检）。 */
  get sweeping(): boolean {
    return this.sweeper !== null;
  }

  set(key: string, value: V, options: SetOptions = {}): void {
    const existing = this.map.get(key);
    if (existing) this.removeEntry(existing);

    const now = this.now();
    const ttlMs = options.ttlMs ?? Infinity;
    const slidingMs = options.slidingMs ?? Infinity;
    const entry: Entry<V> = {
      key,
      value,
      prev: null,
      next: null,
      absoluteExpiry: now + ttlMs,
      slidingMs,
      expiresAt: Math.min(now + ttlMs, now + slidingMs),
      heapIndex: -1,
    };

    this.map.set(key, entry);
    this.linkFront(entry);
    if (entry.expiresAt !== Infinity) this.heapPush(entry);

    while (this.map.size > this.maxSize && this.tail) {
      this.removeEntry(this.tail);
    }
  }

  /** 读取：惰性判定过期（超时立即返回空并摘除），命中则 O(1) 提升热度并滑动续期。 */
  get(key: string): V | undefined {
    const entry = this.map.get(key);
    if (!entry) return undefined;

    const now = this.now();
    if (entry.expiresAt <= now) {
      this.removeEntry(entry);
      return undefined;
    }

    this.promote(entry, now);
    return entry.value;
  }

  /** 存在性检查：判定过期但不提升热度、不续期。 */
  has(key: string): boolean {
    const entry = this.map.get(key);
    if (!entry) return false;
    if (entry.expiresAt <= this.now()) {
      this.removeEntry(entry);
      return false;
    }
    return true;
  }

  delete(key: string): boolean {
    const entry = this.map.get(key);
    if (!entry) return false;
    this.removeEntry(entry);
    return true;
  }

  /** 批量剔除已过期死对象，返回剔除数量。k 个过期对象耗时 O(k log n)。 */
  sweepExpired(): number {
    const now = this.now();
    let removed = 0;
    while (this.heap.length > 0 && this.heap[0].expiresAt <= now) {
      this.removeEntry(this.heap[0]);
      removed++;
    }
    return removed;
  }

  clear(): void {
    this.map.clear();
    this.heap.length = 0;
    this.head = null;
    this.tail = null;
  }

  /** 停止后台清扫器并清空缓存，释放全部资源。 */
  dispose(): void {
    if (this.sweeper !== null) {
      clearInterval(this.sweeper);
      this.sweeper = null;
    }
    this.clear();
  }

  /** O(1) 热度提升 + 滑动续期（续期后堆内下滤 O(log n)）。 */
  private promote(entry: Entry<V>, now: number): void {
    this.unlink(entry);
    this.linkFront(entry);
    if (entry.slidingMs !== Infinity) {
      const renewed = Math.min(entry.absoluteExpiry, now + entry.slidingMs);
      if (renewed > entry.expiresAt) {
        entry.expiresAt = renewed;
        this.siftDown(entry.heapIndex);
      }
    }
  }

  private removeEntry(entry: Entry<V>): void {
    this.map.delete(entry.key);
    this.unlink(entry);
    if (entry.heapIndex !== -1) this.heapRemoveAt(entry.heapIndex);
  }

  // ---- 热度层：双向链表 ----

  private linkFront(entry: Entry<V>): void {
    entry.prev = null;
    entry.next = this.head;
    if (this.head) this.head.prev = entry;
    this.head = entry;
    if (!this.tail) this.tail = entry;
  }

  private unlink(entry: Entry<V>): void {
    if (entry.prev) entry.prev.next = entry.next;
    else this.head = entry.next;
    if (entry.next) entry.next.prev = entry.prev;
    else this.tail = entry.prev;
    entry.prev = null;
    entry.next = null;
  }

  // ---- 时间层：小顶堆 ----

  private heapPush(entry: Entry<V>): void {
    entry.heapIndex = this.heap.length;
    this.heap.push(entry);
    this.siftUp(entry.heapIndex);
  }

  private heapRemoveAt(index: number): void {
    const removed = this.heap[index];
    const last = this.heap.pop()!;
    removed.heapIndex = -1;
    if (index < this.heap.length) {
      this.heap[index] = last;
      last.heapIndex = index;
      this.siftUp(index);
      this.siftDown(last.heapIndex);
    }
  }

  private siftUp(index: number): void {
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (this.heap[parent].expiresAt <= this.heap[index].expiresAt) break;
      this.swapHeap(parent, index);
      index = parent;
    }
  }

  private siftDown(index: number): void {
    const n = this.heap.length;
    for (;;) {
      let smallest = index;
      const left = index * 2 + 1;
      const right = left + 1;
      if (left < n && this.heap[left].expiresAt < this.heap[smallest].expiresAt) smallest = left;
      if (right < n && this.heap[right].expiresAt < this.heap[smallest].expiresAt) smallest = right;
      if (smallest === index) break;
      this.swapHeap(smallest, index);
      index = smallest;
    }
  }

  private swapHeap(a: number, b: number): void {
    const ea = this.heap[a];
    this.heap[a] = this.heap[b];
    this.heap[b] = ea;
    this.heap[a].heapIndex = a;
    this.heap[b].heapIndex = b;
  }
}
