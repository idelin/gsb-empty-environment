import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { LSN } from './log-record.ts';
import type { LogManager } from './log-manager.ts';

export interface Page {
  pageId: number;
  /** LSN of the most recent log record applied to this page. */
  pageLSN: LSN;
  slots: string[];
  dirty: boolean;
  /** Oldest LSN that dirtied the page since last flush (0 = clean). */
  recLSN: LSN;
}

function clonePage(page: Page): Page {
  return { ...page, slots: [...page.slots] };
}

/**
 * Storage engine with a volatile buffer pool (`cache`) over a durable
 * `disk`. `crash()` drops the buffer pool; only pages explicitly flushed
 * survive. Enforces the WAL rule: a dirty page may not reach disk before
 * the log records describing its contents (up to pageLSN) are forced.
 */
export class StorageEngine {
  private readonly log: LogManager;
  private readonly cache = new Map<number, Page>();
  private readonly disk = new Map<number, Page>();
  readonly slotsPerPage: number;
  private readonly pagesFile: string | null;

  constructor(
    log: LogManager,
    opts?: { slotsPerPage?: number; pagesFile?: string },
  ) {
    this.log = log;
    this.slotsPerPage = opts?.slotsPerPage ?? 8;
    this.pagesFile = opts?.pagesFile ?? null;
    if (this.pagesFile && existsSync(this.pagesFile)) {
      const entries = JSON.parse(readFileSync(this.pagesFile, 'utf8')) as [
        number,
        Page,
      ][];
      for (const [id, page] of entries) this.disk.set(id, page);
    }
  }

  fetchPage(pageId: number): Page {
    let page = this.cache.get(pageId);
    if (!page) {
      const onDisk = this.disk.get(pageId);
      page = onDisk
        ? clonePage(onDisk)
        : {
            pageId,
            pageLSN: 0,
            slots: new Array<string>(this.slotsPerPage).fill(''),
            dirty: false,
            recLSN: 0,
          };
      this.cache.set(pageId, page);
    }
    return page;
  }

  readSlot(pageId: number, offset: number): string {
    return this.fetchPage(pageId).slots[offset] ?? '';
  }

  writeSlot(pageId: number, offset: number, value: string, lsn: LSN): void {
    const page = this.fetchPage(pageId);
    page.slots[offset] = value;
    page.pageLSN = lsn;
    if (!page.dirty) {
      page.dirty = true;
      page.recLSN = lsn;
    }
  }

  /** WAL rule: force log up to pageLSN before the page hits disk. */
  flushPage(pageId: number): void {
    const page = this.cache.get(pageId);
    if (!page || !page.dirty) return;
    this.log.flush(page.pageLSN);
    this.disk.set(pageId, clonePage(page));
    page.dirty = false;
    page.recLSN = 0;
    this.persistDisk();
  }

  flushAll(): void {
    for (const pageId of [...this.cache.keys()]) this.flushPage(pageId);
    this.log.flush();
  }

  /** DirtyPageTable view: pageId -> recLSN for every dirty buffered page. */
  dirtyPageTable(): Map<number, LSN> {
    const dpt = new Map<number, LSN>();
    for (const page of this.cache.values()) {
      if (page.dirty) dpt.set(page.pageId, page.recLSN);
    }
    return dpt;
  }

  /** Simulate a crash: the volatile buffer pool is lost, disk survives. */
  crash(): void {
    this.cache.clear();
  }

  readDiskSlot(pageId: number, offset: number): string {
    return this.disk.get(pageId)?.slots[offset] ?? '';
  }

  diskPageLSN(pageId: number): LSN {
    return this.disk.get(pageId)?.pageLSN ?? 0;
  }

  diskSnapshot(): Record<number, string[]> {
    const snap: Record<number, string[]> = {};
    for (const [id, page] of [...this.disk.entries()].sort(
      (a, b) => a[0] - b[0],
    )) {
      snap[id] = [...page.slots];
    }
    return snap;
  }

  private persistDisk(): void {
    if (!this.pagesFile) return;
    writeFileSync(this.pagesFile, JSON.stringify([...this.disk.entries()]));
  }
}
