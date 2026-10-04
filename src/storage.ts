import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import type { DirtyPageTable, LSN, PageId } from './types.ts';

export interface Page {
  pageId: PageId;
  data: string;
  pageLSN: LSN;
}

export function pageFileName(pageId: PageId): string {
  return `page-${pageId}.json`;
}

/** Page-oriented stable storage: one JSON file per page, atomic via tmp+rename. */
export class DiskStorage {
  readonly dir: string;

  constructor(dir: string) {
    this.dir = dir;
    mkdirSync(dir, { recursive: true });
  }

  pathFor(pageId: PageId): string {
    return join(this.dir, pageFileName(pageId));
  }

  readPage(pageId: PageId): Page | null {
    const file = this.pathFor(pageId);
    if (!existsSync(file)) return null;
    return JSON.parse(readFileSync(file, 'utf8')) as Page;
  }

  writePage(page: Page): void {
    const file = this.pathFor(page.pageId);
    const tmp = file + '.tmp';
    writeFileSync(tmp, JSON.stringify(page));
    renameSync(tmp, file);
  }

  listPageIds(): PageId[] {
    if (!existsSync(this.dir)) return [];
    return readdirSync(this.dir)
      .filter((f) => /^page-\d+\.json$/.test(f))
      .map((f) => Number(f.slice('page-'.length, -'.json'.length)))
      .sort((a, b) => a - b);
  }
}

/**
 * Called before the first in-pool modification of a page. Receives a copy of
 * the pre-modification image, or `null` when the page is being created.
 * Used by the snapshot manager to implement copy-on-write.
 */
export type BeforeModifyHook = (pageId: PageId, current: Page | null) => void;

/**
 * Buffer pool with steal/no-force semantics: dirty pages may be written back
 * at any time (steal) and are not forced at commit (no-force). The WAL-ahead
 * rule is enforced by `logFlusher`, invoked before any page hits disk.
 */
export class BufferPool {
  private readonly storage: DiskStorage;
  private readonly logFlusher: (() => void) | null;
  private readonly frames = new Map<PageId, Page>();
  /** pageId -> recLSN: the oldest LSN that dirtied the resident page. */
  private readonly dirtySince = new Map<PageId, LSN>();
  private readonly hooks = new Set<BeforeModifyHook>();

  constructor(storage: DiskStorage, logFlusher: (() => void) | null = null) {
    this.storage = storage;
    this.logFlusher = logFlusher;
  }

  registerBeforeModifyHook(hook: BeforeModifyHook): () => void {
    this.hooks.add(hook);
    return () => {
      this.hooks.delete(hook);
    };
  }

  /**
   * Fetch a page, loading from disk when necessary. A page that exists
   * nowhere yields a transient empty image that is NOT cached, so merely
   * reading a page never materializes it in the pool.
   */
  get(pageId: PageId): Page {
    const resident = this.frames.get(pageId);
    if (resident) return resident;
    const loaded = this.storage.readPage(pageId);
    if (loaded) {
      this.frames.set(pageId, loaded);
      return loaded;
    }
    return { pageId, data: '', pageLSN: 0 };
  }

  /** Current image without polluting the pool: buffer first, then disk. */
  peek(pageId: PageId): Page | null {
    const resident = this.frames.get(pageId);
    if (resident) return { ...resident };
    return this.storage.readPage(pageId);
  }

  cachedPageIds(): PageId[] {
    return [...this.frames.keys()].sort((a, b) => a - b);
  }

  /** Apply a new image at the given LSN, marking the page dirty. */
  updatePage(pageId: PageId, data: string, lsn: LSN): void {
    const existing = this.frames.get(pageId) ?? this.storage.readPage(pageId);
    for (const hook of this.hooks) {
      hook(pageId, existing ? { ...existing } : null);
    }
    const page = existing ?? { pageId, data: '', pageLSN: 0 };
    page.data = data;
    page.pageLSN = lsn;
    this.frames.set(pageId, page);
    if (!this.dirtySince.has(pageId)) this.dirtySince.set(pageId, lsn);
  }

  flushPage(pageId: PageId): void {
    const page = this.frames.get(pageId);
    if (!page || !this.dirtySince.has(pageId)) return;
    if (this.logFlusher) this.logFlusher(); // WAL-ahead rule
    this.storage.writePage(page);
    this.dirtySince.delete(pageId);
  }

  flushAll(): void {
    for (const pageId of [...this.dirtySince.keys()]) this.flushPage(pageId);
  }

  /** Copy of the dirty-page table: pageId -> recLSN. */
  dirtyPageTable(): DirtyPageTable {
    return new Map(this.dirtySince);
  }

  get dirtyCount(): number {
    return this.dirtySince.size;
  }
}
