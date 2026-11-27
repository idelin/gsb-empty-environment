/**
 * Bounded LRU cache keyed by series identity.
 *
 * Iteration order of an ES `Map` follows insertion order; "touching" an entry
 * deletes and re-inserts it so the head of the map is always the
 * least-recently-updated series and the tail the most-recently-updated one.
 */
export class LruCache<V> {
  private readonly entries = new Map<string, V>();
  private readonly capacity: number | undefined;

  constructor(capacity?: number) {
    if (capacity !== undefined && (!Number.isInteger(capacity) || capacity <= 0)) {
      throw new Error('maxSeries must be a positive integer when provided');
    }
    this.capacity = capacity;
  }

  get size(): number {
    return this.entries.size;
  }

  get(key: string): V | undefined {
    const value = this.entries.get(key);
    if (value === undefined) {
      return undefined;
    }
    this.entries.delete(key);
    this.entries.set(key, value);
    return value;
  }

  has(key: string): boolean {
    return this.entries.has(key);
  }

  /**
   * Insert or refresh a series, evicting the least-recently-updated entry
   * first when the cache is at capacity. Returns the evicted key/value pair
   * (if any) so callers can account for it.
   */
  set(key: string, value: V): { evictedKey: string; evictedValue: V } | undefined {
    const existing = this.entries.get(key);
    if (existing !== undefined) {
      this.entries.delete(key);
      this.entries.set(key, value);
      return undefined;
    }

    let evicted: { evictedKey: string; evictedValue: V } | undefined;
    if (this.capacity !== undefined && this.entries.size >= this.capacity) {
      const oldest = this.entries.keys().next();
      if (!oldest.done && oldest.value !== undefined) {
        const evictedKey = oldest.value;
        const evictedValue = this.entries.get(evictedKey) as V;
        this.entries.delete(evictedKey);
        evicted = { evictedKey, evictedValue };
      }
    }
    this.entries.set(key, value);
    return evicted;
  }

  delete(key: string): boolean {
    return this.entries.delete(key);
  }

  clear(): void {
    this.entries.clear();
  }

  keys(): IterableIterator<string> {
    return this.entries.keys();
  }
}
