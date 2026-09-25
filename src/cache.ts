/**
 * Generation-tagged LRU cache with a byte budget.
 *
 * - Entry cost is the *actual* size reported by the caller-supplied `sizeof`.
 * - Eviction never removes borrowed (pinned) entries; pinned bytes may push
 *   `usedBytes` past the budget, but accounting always stays exact.
 * - `swapGeneration()` atomically replaces the live map: every entry of the
 *   old generation disappears in one step, and stale async writers that still
 *   hold the old generation number fail the generation check on `set`.
 */

export interface CacheEntry<V> {
  key: string;
  value: V;
  size: number;
  generation: number;
  borrows: number;
}

export interface BorrowHandle<V> {
  readonly key: string;
  readonly value: V;
  release(): void;
}

export class GenerationCache<V> {
  private entries = new Map<string, CacheEntry<V>>();
  private generation = 0;
  private used = 0;
  readonly budgetBytes: number;
  private readonly sizeof: (value: V) => number;

  constructor(budgetBytes: number, sizeof: (value: V) => number) {
    if (budgetBytes <= 0) throw new Error("budgetBytes must be positive");
    this.budgetBytes = budgetBytes;
    this.sizeof = sizeof;
  }

  get currentGeneration(): number {
    return this.generation;
  }

  get usedBytes(): number {
    return this.used;
  }

  get size(): number {
    return this.entries.size;
  }

  /** Byte-accounting invariant: tracked bytes equal the sum of entry sizes. */
  checkConservation(): boolean {
    let sum = 0;
    for (const entry of this.entries.values()) sum += entry.size;
    return sum === this.used;
  }

  get(key: string): V | undefined {
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    // LRU touch: re-insert to move to the most-recently-used end.
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value;
  }

  peek(key: string): V | undefined {
    return this.entries.get(key)?.value;
  }

  /**
   * Insert only when `generation` matches the live generation. A stale async
   * producer holding an older generation number loses the race and its value
   * is dropped instead of overwriting newer data.
   *
   * Eviction never removes the entry being inserted: if borrowed entries
   * pin the cache over budget, the new entry stays and accounting simply
   * runs over budget until borrows are released.
   */
  set(key: string, value: V, generation: number): boolean {
    if (generation !== this.generation) return false;
    const existing = this.entries.get(key);
    if (existing) this.removeEntry(existing);
    const size = this.sizeof(value);
    const entry: CacheEntry<V> = { key, value, size, generation, borrows: 0 };
    this.entries.set(key, entry);
    this.used += size;
    this.evictWithinBudget(key);
    return true;
  }

  /** Pin an entry so eviction cannot reclaim it while the handle is alive. */
  borrow(key: string): BorrowHandle<V> | undefined {
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    entry.borrows += 1;
    let released = false;
    return {
      key,
      value: entry.value,
      release: () => {
        if (released) return;
        released = true;
        entry.borrows -= 1;
        this.evictWithinBudget();
      },
    };
  }

  isBorrowed(key: string): boolean {
    return (this.entries.get(key)?.borrows ?? 0) > 0;
  }

  /**
   * Atomic generation switch: the whole live map is replaced in one step.
   * Borrowed entries of the old generation are released with it — a borrow
   * never outlives its generation.
   */
  swapGeneration(): number {
    this.entries = new Map();
    this.used = 0;
    this.generation += 1;
    return this.generation;
  }

  private removeEntry(entry: CacheEntry<V>): void {
    this.entries.delete(entry.key);
    this.used -= entry.size;
  }

  private evictWithinBudget(protectedKey?: string): void {
    for (const entry of this.entries.values()) {
      if (this.used <= this.budgetBytes) break;
      if (entry.borrows > 0) continue; // borrowed entries are never evicted
      if (entry.key === protectedKey) continue; // never evict the new insert
      this.removeEntry(entry);
    }
  }
}
