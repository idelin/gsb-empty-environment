/**
 * Zero-allocation fixed-capacity cache.
 *
 * Backed by three preallocated flat arrays (presence flags, keys, values)
 * with open addressing + linear probing. After construction the hot path
 * (get / getOrLoad hit) performs no heap allocation at all: no closures
 * retained per entry, no boxing of keys, no array growth.
 *
 * Eviction: when full, the probe's first slot is reclaimed (direct-mapped
 * fallback), keeping insert O(1)-ish and allocation-free.
 */
export interface CacheStats {
  capacity: number;
  size: number;
  hits: number;
  misses: number;
  evictions: number;
}

export class ZeroAllocCache<V> {
  readonly capacity: number;
  private readonly present: Uint8Array;
  private readonly keys: Float64Array;
  private readonly values: (V | undefined)[];
  private sizeCount = 0;
  private hitCount = 0;
  private missCount = 0;
  private evictionCount = 0;

  constructor(capacity = 256) {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError("capacity must be a positive integer");
    }
    this.capacity = capacity;
    this.present = new Uint8Array(capacity);
    this.keys = new Float64Array(capacity);
    this.values = new Array<V | undefined>(capacity);
  }

  get size(): number {
    return this.sizeCount;
  }

  private slotOf(key: number): number {
    // Knuth multiplicative hash, constant-time, no allocation.
    const h = (key * 2654435761) >>> 0;
    return h % this.capacity;
  }

  /** Lookup without inserting. Returns undefined on miss. */
  get(key: number): V | undefined {
    const start = this.slotOf(key);
    for (let i = 0; i < this.capacity; i++) {
      const slot = (start + i) % this.capacity;
      if (this.present[slot] === 0) {
        this.missCount++;
        return undefined;
      }
      if (this.keys[slot] === key) {
        this.hitCount++;
        return this.values[slot];
      }
    }
    this.missCount++;
    return undefined;
  }

  /**
   * Lookup; on miss compute via `loader`, insert (evicting the home slot
   * occupant if the table is full along the probe path) and return.
   */
  getOrLoad(key: number, loader: (key: number) => V): V {
    const start = this.slotOf(key);
    for (let i = 0; i < this.capacity; i++) {
      const slot = (start + i) % this.capacity;
      if (this.present[slot] === 0) {
        const v = loader(key);
        this.missCount++;
        this.present[slot] = 1;
        this.keys[slot] = key;
        this.values[slot] = v;
        this.sizeCount++;
        return v;
      }
      if (this.keys[slot] === key) {
        this.hitCount++;
        return this.values[slot] as V;
      }
    }
    // Table full along the whole ring: reclaim the home slot.
    const v = loader(key);
    this.missCount++;
    this.evictionCount++;
    this.keys[start] = key;
    this.values[start] = v;
    return v;
  }

  clear(): void {
    this.present.fill(0);
    this.values.fill(undefined);
    this.sizeCount = 0;
  }

  stats(): CacheStats {
    return {
      capacity: this.capacity,
      size: this.sizeCount,
      hits: this.hitCount,
      misses: this.missCount,
      evictions: this.evictionCount,
    };
  }
}
