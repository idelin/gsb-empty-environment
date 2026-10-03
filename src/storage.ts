import type { LSN, PageId } from './types.ts';
import type { LogManager } from './wal.ts';

export interface Page {
  pageLSN: LSN;
  data: Record<string, number>;
}

// Persistent storage. Survives crashes.
export class Disk {
  private pages = new Map<PageId, Page>();

  readPage(pageId: PageId): Page {
    const page = this.pages.get(pageId);
    return page ? { pageLSN: page.pageLSN, data: { ...page.data } } : { pageLSN: -1, data: {} };
  }

  writePage(pageId: PageId, page: Page): void {
    this.pages.set(pageId, { pageLSN: page.pageLSN, data: { ...page.data } });
  }

  pageIds(): PageId[] {
    return [...this.pages.keys()].sort((a, b) => a - b);
  }
}

interface Frame {
  page: Page;
  dirty: boolean;
  recLSN: LSN | null;
}

// Volatile buffer pool. steal/no-force: dirty frames may be evicted at any
// time (steal) and commit never forces pages to disk (no-force).
// WAL protocol: before a dirty page is written to disk, the log must be
// durable up to the page's pageLSN.
export class BufferPool {
  private disk: Disk;
  private capacity: number;
  private log: LogManager | null;
  private frames = new Map<PageId, Frame>(); // Map order == LRU order

  constructor(disk: Disk, capacity = 8, log: LogManager | null = null) {
    this.disk = disk;
    this.capacity = capacity;
    this.log = log;
  }

  fetch(pageId: PageId): Page {
    let frame = this.frames.get(pageId);
    if (!frame) {
      if (this.frames.size >= this.capacity) this.evict();
      frame = { page: this.disk.readPage(pageId), dirty: false, recLSN: null };
      this.frames.set(pageId, frame);
    } else {
      this.frames.delete(pageId);
      this.frames.set(pageId, frame);
    }
    return frame.page;
  }

  update(pageId: PageId, key: string, value: number, lsn: LSN): void {
    const page = this.fetch(pageId);
    page.data[key] = value;
    page.pageLSN = lsn;
    const frame = this.frames.get(pageId);
    if (frame && !frame.dirty) {
      frame.dirty = true;
      frame.recLSN = lsn;
    }
  }

  flushPage(pageId: PageId): boolean {
    const frame = this.frames.get(pageId);
    if (!frame || !frame.dirty) return false;
    if (this.log) this.log.flush(frame.page.pageLSN); // WAL before data
    this.disk.writePage(pageId, frame.page);
    frame.dirty = false;
    frame.recLSN = null;
    return true;
  }

  flushAll(): void {
    for (const pageId of [...this.frames.keys()]) this.flushPage(pageId);
  }

  flushRandomDirty(rand: { int(lo: number, hi: number): number }): boolean {
    const dirtyIds = [...this.frames.keys()].filter((id) => this.frames.get(id)?.dirty);
    if (dirtyIds.length === 0) return false;
    return this.flushPage(dirtyIds[rand.int(0, dirtyIds.length - 1)]);
  }

  private evict(): void {
    const oldest = this.frames.keys().next();
    if (oldest.done) return;
    const pageId = oldest.value;
    this.flushPage(pageId); // steal: uncommitted data may reach disk
    this.frames.delete(pageId);
  }

  dirtyPageTable(): Array<{ pageId: PageId; recLSN: LSN }> {
    const table: Array<{ pageId: PageId; recLSN: LSN }> = [];
    for (const [pageId, frame] of this.frames) {
      if (frame.dirty && frame.recLSN !== null) table.push({ pageId, recLSN: frame.recLSN });
    }
    return table;
  }

  allClean(): boolean {
    for (const frame of this.frames.values()) if (frame.dirty) return false;
    return true;
  }

  get size(): number {
    return this.frames.size;
  }

  discardAll(): void {
    this.frames.clear();
  }
}
