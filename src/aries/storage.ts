import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { MetricsRegistry } from '../metrics/registry.ts';
import type { DirtyPageEntry } from './types.ts';

export interface Page {
  pageId: string;
  pageLSN: number;
  value: unknown;
}

interface CachedPage extends Page {
  dirty: boolean;
}

/**
 * Durable page store backed by a single JSON file. Every `writePage`
 * persists synchronously, so whatever reaches this class survives a crash.
 */
export class DiskStorage {
  private readonly path: string;
  private readonly pages = new Map<string, Page>();

  constructor(path: string) {
    this.path = path;
    if (existsSync(path)) {
      const raw = readFileSync(path, 'utf8');
      if (raw.trim() !== '') {
        for (const page of JSON.parse(raw) as Page[]) {
          this.pages.set(page.pageId, { ...page });
        }
      }
    }
  }

  readPage(pageId: string): Page | undefined {
    const page = this.pages.get(pageId);
    return page ? { ...page } : undefined;
  }

  writePage(page: Page): void {
    this.pages.set(page.pageId, {
      pageId: page.pageId,
      pageLSN: page.pageLSN,
      value: page.value,
    });
    this.persist();
  }

  snapshot(): Page[] {
    return [...this.pages.values()].map((p) => ({ ...p }));
  }

  private persist(): void {
    writeFileSync(this.path, JSON.stringify([...this.pages.values()]), 'utf8');
  }
}

/**
 * Capacity-bounded LRU buffer pool over DiskStorage. Tracks per-page dirty
 * state and the recLSN (first LSN that dirtied a clean page) needed to build
 * the DirtyPageTable at checkpoint time. Hit/miss counts feed the metrics
 * registry when one is attached.
 */
export class BufferPool {
  private readonly storage: DiskStorage;
  private readonly capacity: number;
  private readonly metrics?: MetricsRegistry;
  private readonly cache = new Map<string, CachedPage>();
  private readonly dirtySince = new Map<string, number>();
  private hits = 0;
  private misses = 0;

  constructor(storage: DiskStorage, capacity = 128, metrics?: MetricsRegistry) {
    if (capacity < 1) throw new Error('buffer pool capacity must be >= 1');
    this.storage = storage;
    this.capacity = capacity;
    this.metrics = metrics;
  }

  /** Fetches a page, creating an empty one (pageLSN 0) if absent on disk. */
  fetch(pageId: string): Page {
    const cached = this.cache.get(pageId);
    if (cached) {
      this.hits++;
      this.metrics?.counter('buffer_pool_hits_total').inc();
      // LRU touch.
      this.cache.delete(pageId);
      this.cache.set(pageId, cached);
      return cached;
    }
    this.misses++;
    this.metrics?.counter('buffer_pool_misses_total').inc();
    const fromDisk = this.storage.readPage(pageId);
    const page: CachedPage = fromDisk
      ? { ...fromDisk, dirty: false }
      : { pageId, pageLSN: 0, value: null, dirty: false };
    this.cache.set(pageId, page);
    this.evictIfNeeded();
    return page;
  }

  markDirty(pageId: string, lsn: number): void {
    const page = this.cache.get(pageId);
    if (!page) throw new Error(`page ${pageId} is not in the buffer pool`);
    if (!page.dirty) {
      page.dirty = true;
      this.dirtySince.set(pageId, lsn);
    }
  }

  flushPage(pageId: string): void {
    const page = this.cache.get(pageId);
    if (page && page.dirty) {
      this.storage.writePage({
        pageId: page.pageId,
        pageLSN: page.pageLSN,
        value: page.value,
      });
      page.dirty = false;
      this.dirtySince.delete(pageId);
    }
  }

  flushAll(): void {
    for (const pageId of [...this.cache.keys()]) {
      this.flushPage(pageId);
    }
  }

  /** DirtyPageTable view: one entry per dirty page with its recLSN. */
  dirtyPages(): DirtyPageEntry[] {
    return [...this.dirtySince.entries()].map(([pageId, recLSN]) => ({
      pageId,
      recLSN,
    }));
  }

  stats(): { hits: number; misses: number; hitRate: number | null } {
    const total = this.hits + this.misses;
    return {
      hits: this.hits,
      misses: this.misses,
      hitRate: total === 0 ? null : this.hits / total,
    };
  }

  private evictIfNeeded(): void {
    while (this.cache.size > this.capacity) {
      const oldest = this.cache.keys().next().value as string;
      this.flushPage(oldest);
      this.cache.delete(oldest);
    }
  }
}
