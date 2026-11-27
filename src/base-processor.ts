import { LruCache } from './lru.js';
import type { ProcessorOptions, ProcessorStats, ResetReason } from './types.js';
import { createEmptyStats } from './types.js';

/**
 * Common scaffolding for the two stateful temporality processors: bounded LRU
 * series storage, eviction bookkeeping (so a re-entrant series is recognizable
 * after it was previously evicted) and lifecycle stats.
 */
export abstract class BaseProcessor {
  protected readonly maxSeries: number | undefined;
  protected readonly initialPointAsDelta: boolean;
  protected readonly onDrop?: ProcessorOptions['onDrop'];
  protected readonly cache: LruCache<unknown>;
  protected readonly evictedKeys: Set<string>;
  protected stats: ProcessorStats = createEmptyStats();

  protected constructor(options: ProcessorOptions = {}) {
    this.maxSeries = options.maxSeries;
    this.initialPointAsDelta = options.initialPointAsDelta ?? false;
    this.onDrop = options.onDrop;
    this.cache = new LruCache(options.maxSeries);
    this.evictedKeys = new Set();
  }

  /**
   * Register whatever series the LRU had to evict. The marker set is bounded
   * so pathological key churn cannot leak memory: it only needs to remember
   * recently evicted identities until they potentially return.
   */
  protected registerEviction(
    result: { evictedKey: string } | undefined,
    markerCapacity: number,
  ): void {
    if (!result) {
      return;
    }
    this.stats.seriesEvicted += 1;
    this.evictedKeys.add(result.evictedKey);
    if (this.evictedKeys.size > markerCapacity) {
      const oldest = this.evictedKeys.keys().next();
      if (!oldest.done && oldest.value !== undefined) {
        this.evictedKeys.delete(oldest.value);
      }
    }
  }

  protected markerCapacity(): number {
    return Math.max(this.maxSeries ?? 1024, 1024);
  }

  /** Returns true when this key belonged to a series evicted earlier. */
  protected consumeReentryMarker(key: string): boolean {
    const reentry = this.evictedKeys.delete(key);
    if (reentry) {
      this.stats.evictionReentries += 1;
    }
    return reentry;
  }

  protected recordReset(reason: ResetReason): void {
    this.stats.resetReasons[reason] += 1;
  }

  getStats(): ProcessorStats {
    return {
      ...this.stats,
      resetReasons: { ...this.stats.resetReasons },
    };
  }

  resetStats(): ProcessorStats {
    const snapshot = this.getStats();
    this.stats = createEmptyStats();
    return snapshot;
  }

  /** Drop all tracked series state (stats are preserved). */
  clearState(): void {
    this.cache.clear();
    this.evictedKeys.clear();
  }

  get seriesCount(): number {
    return this.cache.size;
  }
}
