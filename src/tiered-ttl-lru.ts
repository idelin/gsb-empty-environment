/**
 * Tiered time-aware (TTL / TTD) LRU dual indexer.
 *
 * Every entry lives in exactly three places that share ONE allocation:
 *   1. `Map<K, CacheNode>`            — O(1) key lookup
 *   2. intrusive doubly linked list   — O(1) hotness promotion (MRU head .. LRU tail)
 *   3. binary min-heap by `expiresAt` — O(log n) deadline maintenance, O(k log n) batch reap
 *
 * Expiration model:
 *   - `ttlMs`     absolute time-to-live, deadline fixed at write time
 *   - `slidingMs` sliding time-to-idle (TTD), deadline refreshed on every access
 *   - both may combine; the effective deadline is always `expiresAt`
 *
 * Reaping is lazy-but-batched: every public operation first pops the whole
 * expired prefix of the heap in one pass, so dead objects are freed in bulk
 * and never linger past the first operation after their deadline.
 */
export interface ExpiryOptions {
  /** Absolute time-to-live in ms, counted from the write. */
  ttlMs?: number;
  /** Sliding expiration window in ms, refreshed on every hit (TTD). */
  slidingMs?: number;
}

export interface CacheStats {
  size: number;
  hits: number;
  misses: number;
  expiredReaped: number;
  lruEvicted: number;
}

interface CacheNode<K, V> {
  key: K;
  value: V;
  prev: CacheNode<K, V> | null;
  next: CacheNode<K, V> | null;
  heapIndex: number;
  expiresAt: number;
  slidingMs: number;
}

export class TieredTtlLruCache<K, V> {
  private readonly maxSize: number;
  private readonly defaultTtlMs: number;
  private readonly defaultSlidingMs: number;

  private readonly map = new Map<K, CacheNode<K, V>>();
  private head: CacheNode<K, V> | null = null;
  private tail: CacheNode<K, V> | null = null;
  private heap: Array<CacheNode<K, V>> = [];

  private hits = 0;
  private misses = 0;
  private expiredReaped = 0;
  private lruEvicted = 0;

  private janitor: ReturnType<typeof setInterval> | null = null;

  constructor(options: {
    maxSize?: number;
    ttlMs?: number;
    slidingMs?: number;
  } = {}) {
    this.maxSize = options.maxSize ?? Number.POSITIVE_INFINITY;
    this.defaultTtlMs = options.ttlMs ?? 0;
    this.defaultSlidingMs = options.slidingMs ?? 0;
    if (this.maxSize < 1) throw new RangeError('maxSize must be >= 1');
  }

  get size(): number {
    this.sweep();
    return this.map.size;
  }

  get stats(): CacheStats {
    return {
      size: this.map.size,
      hits: this.hits,
      misses: this.misses,
      expiredReaped: this.expiredReaped,
      lruEvicted: this.lruEvicted,
    };
  }

  /**
   * Read with hotness promotion and sliding-deadline refresh.
   * Returns `undefined` for missing or already-dead entries; a dead entry is
   * unlinked from all three indexes immediately (no zombie references).
   */
  get(key: K): V | undefined {
    const now = Date.now();
    this.sweep(now);
    const node = this.map.get(key);
    if (!node) {
      this.misses++;
      return undefined;
    }
    if (node.expiresAt <= now) {
      // Defensive: sweep() should already have caught this, but a read must
      // never observe a timed-out object even if the clock moved mid-call.
      this.unlink(node);
      this.expiredReaped++;
      this.misses++;
      return undefined;
    }
    this.hits++;
    this.promote(node);
    if (node.slidingMs > 0) {
      node.expiresAt = now + node.slidingMs;
      this.heapFix(node);
    }
    return node.value;
  }

  /** Existence check without hotness promotion or sliding refresh. */
  peek(key: K): V | undefined {
    const now = Date.now();
    this.sweep(now);
    const node = this.map.get(key);
    if (!node || node.expiresAt <= now) return undefined;
    return node.value;
  }

  has(key: K): boolean {
    return this.peek(key) !== undefined;
  }

  set(key: K, value: V, options: ExpiryOptions = {}): void {
    const now = Date.now();
    this.sweep(now);

    const ttlMs = options.ttlMs ?? this.defaultTtlMs;
    const slidingMs = options.slidingMs ?? this.defaultSlidingMs;
    const expiresAt = this.computeDeadline(now, ttlMs, slidingMs);

    const existing = this.map.get(key);
    if (existing) {
      existing.value = value;
      existing.slidingMs = slidingMs;
      existing.expiresAt = expiresAt;
      this.promote(existing);
      this.heapFix(existing);
    } else {
      const node: CacheNode<K, V> = {
        key,
        value,
        prev: null,
        next: null,
        heapIndex: -1,
        expiresAt,
        slidingMs,
      };
      this.map.set(key, node);
      this.insertFront(node);
      this.heapPush(node);
    }

    while (this.map.size > this.maxSize) {
      this.evictColdest();
    }
  }

  delete(key: K): boolean {
    const node = this.map.get(key);
    if (!node) return false;
    this.unlink(node);
    return true;
  }

  /** Keys ordered hottest -> coldest. Expired entries are reaped first. */
  keys(): K[] {
    this.sweep();
    const out: K[] = [];
    for (let n = this.head; n; n = n.next) out.push(n.key);
    return out;
  }

  /**
   * Batch-reap every entry whose deadline has passed. Runs automatically at
   * the top of each operation; exposed for explicit janitorial passes.
   * Returns the number of dead objects removed in this batch.
   */
  sweep(now: number = Date.now()): number {
    let reaped = 0;
    while (this.heap.length > 0 && this.heap[0].expiresAt <= now) {
      const dead = this.heapPop();
      this.detachFromList(dead);
      this.map.delete(dead.key);
      reaped++;
    }
    this.expiredReaped += reaped;
    return reaped;
  }

  /** Milliseconds until the next deadline, or `Infinity` if nothing can expire. */
  nextExpiryIn(now: number = Date.now()): number {
    return this.heap.length === 0 ? Infinity : this.heap[0].expiresAt - now;
  }

  /**
   * Optional background janitor for fully idle periods. The timer is
   * `unref()`ed so it never keeps the process alive.
   */
  startJanitor(intervalMs: number): void {
    this.stopJanitor();
    this.janitor = setInterval(() => this.sweep(), intervalMs);
    this.janitor.unref?.();
  }

  stopJanitor(): void {
    if (this.janitor) {
      clearInterval(this.janitor);
      this.janitor = null;
    }
  }

  /** Drop everything and release all references (zero-leak teardown). */
  dispose(): void {
    this.stopJanitor();
    this.map.clear();
    this.heap = [];
    this.head = null;
    this.tail = null;
  }

  /** Structural invariant check used by tests: all three indexes agree. */
  verifyIntegrity(): boolean {
    if (this.map.size !== this.heap.length) return false;
    let count = 0;
    for (let n = this.head; n; n = n.next) count++;
    if (count !== this.map.size) return false;
    for (let i = 0; i < this.heap.length; i++) {
      const node = this.heap[i];
      if (node.heapIndex !== i) return false;
      const left = 2 * i + 1;
      const right = 2 * i + 2;
      if (left < this.heap.length && this.heap[left].expiresAt < node.expiresAt) return false;
      if (right < this.heap.length && this.heap[right].expiresAt < node.expiresAt) return false;
    }
    return true;
  }

  private computeDeadline(now: number, ttlMs: number, slidingMs: number): number {
    const candidates: number[] = [];
    if (ttlMs > 0) candidates.push(now + ttlMs);
    if (slidingMs > 0) candidates.push(now + slidingMs);
    return candidates.length === 0 ? Infinity : Math.min(...candidates);
  }

  private evictColdest(): void {
    const victim = this.tail;
    if (!victim) return;
    this.unlink(victim);
    this.lruEvicted++;
  }

  private unlink(node: CacheNode<K, V>): void {
    this.map.delete(node.key);
    this.detachFromList(node);
    this.heapRemove(node);
  }

  private insertFront(node: CacheNode<K, V>): void {
    node.prev = null;
    node.next = this.head;
    if (this.head) this.head.prev = node;
    this.head = node;
    if (!this.tail) this.tail = node;
  }

  private detachFromList(node: CacheNode<K, V>): void {
    if (node.prev) node.prev.next = node.next;
    else this.head = node.next;
    if (node.next) node.next.prev = node.prev;
    else this.tail = node.prev;
    node.prev = null;
    node.next = null;
  }

  private promote(node: CacheNode<K, V>): void {
    if (node === this.head) return;
    this.detachFromList(node);
    this.insertFront(node);
  }

  private heapPush(node: CacheNode<K, V>): void {
    node.heapIndex = this.heap.length;
    this.heap.push(node);
    this.siftUp(node.heapIndex);
  }

  private heapPop(): CacheNode<K, V> {
    const top = this.heap[0];
    const last = this.heap.pop()!;
    if (this.heap.length > 0) {
      this.heap[0] = last;
      last.heapIndex = 0;
      this.siftDown(0);
    }
    top.heapIndex = -1;
    return top;
  }

  private heapRemove(node: CacheNode<K, V>): void {
    const index = node.heapIndex;
    if (index < 0) return;
    const last = this.heap.pop()!;
    if (index < this.heap.length) {
      this.heap[index] = last;
      last.heapIndex = index;
      this.heapFix(last);
    }
    node.heapIndex = -1;
  }

  private heapFix(node: CacheNode<K, V>): void {
    const index = node.heapIndex;
    if (index < 0) return;
    if (index > 0) {
      const parent = (index - 1) >> 1;
      if (this.heap[parent].expiresAt > node.expiresAt) {
        this.siftUp(index);
        return;
      }
    }
    this.siftDown(index);
  }

  private siftUp(index: number): void {
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (this.heap[parent].expiresAt <= this.heap[index].expiresAt) break;
      this.heapSwap(parent, index);
      index = parent;
    }
  }

  private siftDown(index: number): void {
    const length = this.heap.length;
    for (;;) {
      const left = 2 * index + 1;
      const right = left + 1;
      let smallest = index;
      if (left < length && this.heap[left].expiresAt < this.heap[smallest].expiresAt) smallest = left;
      if (right < length && this.heap[right].expiresAt < this.heap[smallest].expiresAt) smallest = right;
      if (smallest === index) break;
      this.heapSwap(smallest, index);
      index = smallest;
    }
  }

  private heapSwap(a: number, b: number): void {
    const na = this.heap[a];
    const nb = this.heap[b];
    this.heap[a] = nb;
    this.heap[b] = na;
    na.heapIndex = b;
    nb.heapIndex = a;
  }
}
