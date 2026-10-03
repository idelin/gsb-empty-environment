/**
 * Storage layer: durable disk + volatile buffer pool.
 *
 * Enforces the core WAL invariant: a dirty page may only be flushed to
 * disk after the log is durable up to that page's pageLSN.
 */

import type { LSN, PageID } from './log-record.ts';
import type { WalManager } from './wal.ts';

export interface Page {
  pageId: PageID;
  /** LSN of the most recent log record that modified this page. */
  pageLSN: LSN;
  data: Record<string, unknown>;
}

export class PageLatchError extends Error {}

/** Exclusive page latch registry; used to prove latch discipline (no leaks). */
export class LatchManager {
  private held = new Map<PageID, symbol>();

  acquire(pageId: PageID, owner: symbol): void {
    if (this.held.has(pageId)) {
      throw new PageLatchError(`page ${pageId} is already latched`);
    }
    this.held.set(pageId, owner);
  }

  release(pageId: PageID, owner: symbol): void {
    if (this.held.get(pageId) !== owner) {
      throw new PageLatchError(`page ${pageId} not latched by this owner`);
    }
    this.held.delete(pageId);
  }

  isLatched(pageId: PageID): boolean {
    return this.held.has(pageId);
  }

  get heldCount(): number {
    return this.held.size;
  }
}

export class Disk {
  private pages = new Map<PageID, Page>();

  readPage(pageId: PageID): Page {
    const p = this.pages.get(pageId);
    if (!p) return { pageId, pageLSN: 0, data: {} };
    // Return a deep copy: disk state must be independent of buffer state.
    return { pageId, pageLSN: p.pageLSN, data: structuredClone(p.data) };
  }

  writePage(page: Page): void {
    this.pages.set(page.pageId, {
      pageId: page.pageId,
      pageLSN: page.pageLSN,
      data: structuredClone(page.data),
    });
  }

  allPages(): ReadonlyMap<PageID, Page> {
    return this.pages;
  }
}

interface Frame {
  page: Page;
  dirty: boolean;
}

export class BufferPool {
  private frames = new Map<PageID, Frame>();
  /** recLSN per dirty page: LSN of the first record that dirtied it since flush. */
  private recLSN = new Map<PageID, LSN>();
  private readonly disk: Disk;
  private readonly wal: WalManager;

  constructor(disk: Disk, wal: WalManager) {
    this.disk = disk;
    this.wal = wal;
  }

  /** Fetch a page into the pool (from disk if not cached). */
  fetch(pageId: PageID): Page {
    let frame = this.frames.get(pageId);
    if (!frame) {
      frame = { page: this.disk.readPage(pageId), dirty: false };
      this.frames.set(pageId, frame);
    }
    return frame.page;
  }

  /** Mark a cached page dirty and stamp it with the modifying record's LSN. */
  markDirty(pageId: PageID, lsn: LSN): void {
    const frame = this.frames.get(pageId);
    if (!frame) throw new Error(`page ${pageId} not in buffer pool`);
    if (!frame.dirty) this.recLSN.set(pageId, lsn);
    frame.dirty = true;
    frame.page.pageLSN = lsn;
  }

  /**
   * Flush a page to disk. WAL protocol: the log must be durable up to the
   * page's pageLSN before the page itself may be written out.
   */
  flushPage(pageId: PageID): void {
    const frame = this.frames.get(pageId);
    if (!frame) return;
    if (frame.page.pageLSN > this.wal.getFlushedLSN()) {
      throw new Error(
        `WAL violation: page ${pageId} pageLSN=${frame.page.pageLSN} ` +
          `> flushedLSN=${this.wal.getFlushedLSN()}`,
      );
    }
    this.disk.writePage(frame.page);
    frame.dirty = false;
    this.recLSN.delete(pageId);
  }

  flushAll(): void {
    for (const pageId of [...this.frames.keys()]) this.flushPage(pageId);
  }

  dirtyPages(): PageID[] {
    return [...this.frames.entries()]
      .filter(([, f]) => f.dirty)
      .map(([id]) => id);
  }

  /** Current dirty page table snapshot (pageID -> recLSN). */
  dirtyPageTable(): ReadonlyMap<PageID, LSN> {
    return new Map(this.recLSN);
  }

  /** Simulate a crash: the entire volatile pool is lost. */
  crash(): void {
    this.frames.clear();
    this.recLSN.clear();
  }
}
