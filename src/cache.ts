/**
 * ZeroAllocCache<V> — direct-mapped, fixed-capacity cache with a strict
 * zero-allocation hot path.
 *
 * Backing storage is two preallocated flat structures:
 *   - keys:   Int32Array (slot numbers; EMPTY = -1)
 *   - values: preallocated Array<V|null>
 *
 * get()/put() never allocate: no closures, no boxes, no arrays. Index is
 * `key & mask` (capacity must be a power of two). Collisions evict in
 * place; the evicted value is handed to the constructor-supplied onEvict
 * callback (typically pool.release), keeping memory fully recycled.
 */

const EMPTY = -1;

export class ZeroAllocCache<V> {
  readonly capacity: number;
  private readonly mask: number;
  private readonly keys: Int32Array;
  private readonly values: (V | null)[];
  private readonly onEvict: ((value: V) => void) | null;

  // Observability counters.
  hits = 0;
  misses = 0;
  evictions = 0;
  private liveCount = 0;

  constructor(capacity: number, onEvict?: (value: V) => void) {
    if (!Number.isInteger(capacity) || capacity <= 0 || (capacity & (capacity - 1)) !== 0) {
      throw new RangeError("capacity must be a positive power of two");
    }
    this.capacity = capacity;
    this.mask = capacity - 1;
    this.keys = new Int32Array(capacity).fill(EMPTY);
    this.values = new Array<V | null>(capacity).fill(null);
    this.onEvict = onEvict ?? null;
  }

  get size(): number {
    return this.liveCount;
  }

  /** Hot path: 2 memory reads, zero allocation. */
  get(key: number): V | undefined {
    const idx = key & this.mask;
    if (this.keys[idx] === key) {
      this.hits++;
      return this.values[idx] as V;
    }
    this.misses++;
    return undefined;
  }

  /**
   * Hot path: in-place write, zero allocation. Overwriting the same key
   * releases the previous value through onEvict (memory reuse, not garbage).
   */
  put(key: number, value: V): void {
    const idx = key & this.mask;
    const existingKey = this.keys[idx];
    if (existingKey === key) {
      const prev = this.values[idx];
      if (prev !== null && prev !== value && this.onEvict !== null) {
        this.onEvict(prev);
      }
      this.values[idx] = value;
      return;
    }
    if (existingKey !== EMPTY) {
      this.evictions++;
      const prev = this.values[idx];
      if (prev !== null && this.onEvict !== null) this.onEvict(prev);
    } else {
      this.liveCount++;
    }
    this.keys[idx] = key;
    this.values[idx] = value;
  }

  delete(key: number): boolean {
    const idx = key & this.mask;
    if (this.keys[idx] !== key) return false;
    const prev = this.values[idx];
    if (prev !== null && this.onEvict !== null) this.onEvict(prev);
    this.keys[idx] = EMPTY;
    this.values[idx] = null;
    this.liveCount--;
    return true;
  }

  clear(): void {
    if (this.onEvict !== null) {
      for (let i = 0; i < this.capacity; i++) {
        if (this.keys[i] !== EMPTY) {
          const prev = this.values[i];
          if (prev !== null) this.onEvict(prev);
        }
      }
    }
    this.keys.fill(EMPTY);
    this.values.fill(null);
    this.liveCount = 0;
  }
}
