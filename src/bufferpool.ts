import type { DiskStorage } from './storage.ts';
import type { Page, PageId, LSN } from './types.ts';
import { deepCopy } from './util.ts';
import type { WAL } from './wal.ts';

/**
 * Volatile buffer pool with steal/no-force semantics: dirty pages may be
 * written to disk at any time (steal) and committed pages are not forced
 * (no-force) — durability comes from the WAL alone. Every page write obeys
 * the WAL-first rule: the log up to pageLSN is flushed before the page.
 */
export class BufferPool {
  private readonly disk: DiskStorage;
  private readonly wal: WAL;
  private readonly frames = new Map<PageId, Page>();
  private readonly dirtyPages = new Set<PageId>();
  /** recLSN per dirty page: LSN of the first update that dirtied it. */
  private readonly recLSNs = new Map<PageId, LSN>();

  constructor(disk: DiskStorage, wal: WAL) {
    this.disk = disk;
    this.wal = wal;
  }

  fetchPage(pageId: PageId): Page {
    let page = this.frames.get(pageId);
    if (!page) {
      const onDisk = this.disk.readPage(pageId);
      page = onDisk ? (deepCopy(onDisk) as Page) : { pageId, pageLSN: 0, data: {} };
      this.frames.set(pageId, page);
    }
    return page;
  }

  applyUpdate(pageId: PageId, key: string, value: unknown, lsn: LSN): void {
    const page = this.fetchPage(pageId);
    page.data[key] = deepCopy(value);
    page.pageLSN = lsn;
    this.noteDirty(pageId, lsn);
  }

  applyDelete(pageId: PageId, key: string, lsn: LSN): void {
    const page = this.fetchPage(pageId);
    delete page.data[key];
    page.pageLSN = lsn;
    this.noteDirty(pageId, lsn);
  }

  /** Installs a page image reconstructed by recovery redo. */
  installDirty(page: Page, recLSN?: LSN): void {
    this.frames.set(page.pageId, page);
    this.noteDirty(page.pageId, recLSN ?? page.pageLSN);
  }

  markDirty(pageId: PageId, lsn: LSN): void {
    this.noteDirty(pageId, lsn);
  }

  private noteDirty(pageId: PageId, lsn: LSN): void {
    if (!this.dirtyPages.has(pageId)) {
      this.dirtyPages.add(pageId);
      this.recLSNs.set(pageId, lsn);
    }
  }

  dirtyPageIds(): PageId[] {
    return [...this.dirtyPages].sort((a, b) => a - b);
  }

  /** Snapshot for checkpoint DPT: pageId -> recLSN (oldest dirtying update). */
  dirtySnapshot(): Record<string, LSN> {
    const out: Record<string, LSN> = {};
    for (const id of this.dirtyPages) {
      out[id] = this.recLSNs.get(id)!;
    }
    return out;
  }

  async flushPage(pageId: PageId): Promise<void> {
    if (!this.dirtyPages.has(pageId)) return;
    const page = this.frames.get(pageId)!;
    await this.wal.flush(page.pageLSN); // WAL-first rule
    this.disk.writePage(page);
    this.dirtyPages.delete(pageId);
    this.recLSNs.delete(pageId);
  }

  async flushAll(): Promise<void> {
    for (const pageId of this.dirtyPageIds()) {
      await this.flushPage(pageId);
    }
  }
}
