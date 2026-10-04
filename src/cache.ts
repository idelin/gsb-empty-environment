/**
 * ZeroAllocCache — bounded LRU cache engineered for the high-frequency
 * read path: get() on a hit performs zero object allocations.
 *
 * Implementation:
 *  - Fixed capacity, slots addressed by integer index.
 *  - LRU order maintained by an intrusive doubly-linked list stored in
 *    preallocated Int32Arrays (no node objects, no GC churn).
 *  - Key lookup via a single Map<string, number>; key strings are stored
 *    by reference (never copied) in a preallocated array.
 *  - Eviction reuses the victim slot in place; the evicted value is handed
 *    to an optional onEvict hook (e.g. released back into an ObjectPool),
 *    so cache+pool together form a closed, allocation-free loop.
 */

export interface CacheStats {
  readonly capacity: number;
  readonly size: number;
  readonly hits: number;
  readonly misses: number;
  readonly evictions: number;
}

const NIL = -1;

export class ZeroAllocCache<V> {
  readonly capacity: number;
  private readonly onEvict?: (key: string, value: V) => void;
  private readonly keys: (string | null)[];
  private readonly values: (V | null)[];
  private readonly prev: Int32Array;
  private readonly next: Int32Array;
  private readonly index: Map<string, number> = new Map();
  private head = NIL; // most recently used slot
  private tail = NIL; // least recently used slot (eviction victim)
  private sizeCount = 0;
  private hitCount = 0;
  private missCount = 0;
  private evictionCount = 0;

  constructor(
    capacity: number,
    onEvict?: (key: string, value: V) => void,
  ) {
    this.capacity = capacity;
    this.onEvict = onEvict;
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError('cache capacity must be an integer >= 1');
    }
    this.keys = new Array<string | null>(capacity).fill(null);
    this.values = new Array<V | null>(capacity).fill(null);
    this.prev = new Int32Array(capacity).fill(NIL);
    this.next = new Int32Array(capacity).fill(NIL);
  }

  get size(): number {
    return this.sizeCount;
  }

  /** Hot path: zero allocations on hit. Returns undefined on miss. */
  get(key: string): V | undefined {
    const slot = this.index.get(key);
    if (slot === undefined) {
      this.missCount++;
      return undefined;
    }
    this.hitCount++;
    this.moveToHead(slot);
    return this.values[slot] as V;
  }

  /** Hot path: zero allocations on hit. */
  peek(key: string): V | undefined {
    const slot = this.index.get(key);
    return slot === undefined ? undefined : (this.values[slot] as V);
  }

  has(key: string): boolean {
    return this.index.has(key);
  }

  /**
   * Insert or overwrite. On a fresh insert beyond capacity the LRU entry is
   * evicted and its slot reused in place. Returns the evicted value (if any)
   * so callers can recycle it; when onEvict is configured the hook has
   * already been invoked and the return value is still provided.
   */
  set(key: string, value: V): V | undefined {
    const existing = this.index.get(key);
    if (existing !== undefined) {
      const old = this.values[existing] as V;
      this.values[existing] = value;
      this.moveToHead(existing);
      return old === value ? undefined : old;
    }

    let slot: number;
    let evicted: V | undefined;
    if (this.sizeCount < this.capacity) {
      slot = this.sizeCount;
      this.sizeCount++;
    } else {
      slot = this.tail;
      const evictedKey = this.keys[slot] as string;
      evicted = this.values[slot] as V;
      this.index.delete(evictedKey);
      this.keys[slot] = null;
      this.values[slot] = null;
      this.detach(slot);
      this.evictionCount++;
      if (this.onEvict !== undefined) this.onEvict(evictedKey, evicted);
    }

    this.keys[slot] = key;
    this.values[slot] = value;
    this.index.set(key, slot);
    this.attachHead(slot);
    return evicted;
  }

  delete(key: string): boolean {
    const slot = this.index.get(key);
    if (slot === undefined) return false;
    this.index.delete(key);
    this.keys[slot] = null;
    this.values[slot] = null;
    this.detach(slot);
    this.sizeCount--;
    // Compact: keep used slots dense in [0, size) so fresh inserts can use
    // slot == sizeCount. Move the last used slot into the freed position.
    const last = this.sizeCount;
    if (slot !== last) {
      const movedKey = this.keys[last] as string;
      this.keys[slot] = movedKey;
      this.values[slot] = this.values[last];
      this.keys[last] = null;
      this.values[last] = null;
      this.prev[slot] = this.prev[last];
      this.next[slot] = this.next[last];
      this.prev[last] = NIL;
      this.next[last] = NIL;
      if (this.prev[slot] !== NIL) this.next[this.prev[slot]] = slot; else this.head = slot;
      if (this.next[slot] !== NIL) this.prev[this.next[slot]] = slot; else this.tail = slot;
      this.index.set(movedKey, slot);
    }
    return true;
  }

  clear(): void {
    this.index.clear();
    this.keys.fill(null);
    this.values.fill(null);
    this.prev.fill(NIL);
    this.next.fill(NIL);
    this.head = NIL;
    this.tail = NIL;
    this.sizeCount = 0;
  }

  /** Iterates from most- to least-recently-used. Test/diagnostic path only. */
  keysMruToLru(): string[] {
    const out: string[] = [];
    let slot = this.head;
    while (slot !== NIL) {
      out.push(this.keys[slot] as string);
      slot = this.next[slot];
    }
    return out;
  }

  get stats(): CacheStats {
    return {
      capacity: this.capacity,
      size: this.sizeCount,
      hits: this.hitCount,
      misses: this.missCount,
      evictions: this.evictionCount,
    };
  }

  private moveToHead(slot: number): void {
    if (slot === this.head) return;
    this.detach(slot);
    this.attachHead(slot);
  }

  private detach(slot: number): void {
    const p = this.prev[slot];
    const n = this.next[slot];
    if (p !== NIL) this.next[p] = n; else this.head = n;
    if (n !== NIL) this.prev[n] = p; else this.tail = p;
    this.prev[slot] = NIL;
    this.next[slot] = NIL;
  }

  private attachHead(slot: number): void {
    this.prev[slot] = NIL;
    this.next[slot] = this.head;
    if (this.head !== NIL) this.prev[this.head] = slot;
    this.head = slot;
    if (this.tail === NIL) this.tail = slot;
  }
}
