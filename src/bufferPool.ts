import type { LogManager } from './logManager.ts';
import type { SimulatedDisk } from './disk.ts';
import type { DirtyPageEntry, LSN, PageId } from './types.ts';

interface Frame {
  pageId: PageId;
  value: number;
  pageLSN: LSN;
  dirty: boolean;
  /** LSN that first dirtied this frame since it was last clean (DPT recLSN). */
  dirtySinceLSN: LSN;
  pinCount: number;
}

/**
 * STEAL + NO-FORCE buffer pool. Dirty pages may be written back at any time
 * (steal) and committed pages are not forced at commit (no-force); the WAL
 * protocol (log flush up to pageLSN before a page write) keeps this safe.
 */
export class BufferPool {
  private frames = new Map<PageId, Frame>();
  private readonly disk: SimulatedDisk;
  private readonly logManager: LogManager;

  constructor(disk: SimulatedDisk, logManager: LogManager) {
    this.disk = disk;
    this.logManager = logManager;
  }

  private fetch(pageId: PageId): Frame {
    let frame = this.frames.get(pageId);
    if (!frame) {
      const page = this.disk.readPage(pageId);
      frame = {
        pageId,
        value: page.value,
        pageLSN: page.pageLSN,
        dirty: false,
        dirtySinceLSN: 0,
        pinCount: 0,
      };
      this.frames.set(pageId, frame);
    }
    return frame;
  }

  read(pageId: PageId): number {
    return this.fetch(pageId).value;
  }

  update(pageId: PageId, value: number, lsn: LSN): void {
    const frame = this.fetch(pageId);
    if (!frame.dirty) frame.dirtySinceLSN = lsn;
    frame.value = value;
    frame.pageLSN = lsn;
    frame.dirty = true;
  }

  pageLSN(pageId: PageId): LSN {
    return this.fetch(pageId).pageLSN;
  }

  pin(pageId: PageId): void {
    this.fetch(pageId).pinCount++;
  }

  unpin(pageId: PageId): void {
    const frame = this.fetch(pageId);
    if (frame.pinCount > 0) frame.pinCount--;
  }

  /** WAL rule: the log is forced up to pageLSN before the page hits disk. */
  flushPage(pageId: PageId): void {
    const frame = this.frames.get(pageId);
    if (!frame || !frame.dirty) return;
    this.logManager.flush(frame.pageLSN);
    this.disk.writePage(pageId, frame.value, frame.pageLSN); // may throw ENOSPC
    frame.dirty = false;
  }

  flushAll(): void {
    for (const pageId of [...this.frames.keys()]) this.flushPage(pageId);
  }

  dirtyPages(): DirtyPageEntry[] {
    const out: DirtyPageEntry[] = [];
    for (const frame of this.frames.values()) {
      if (frame.dirty) out.push({ pageId: frame.pageId, recLSN: frame.dirtySinceLSN });
    }
    return out.sort((a, b) => a.pageId - b.pageId);
  }

  get dirtyCount(): number {
    return this.dirtyPages().length;
  }

  get pinnedCount(): number {
    let total = 0;
    for (const frame of this.frames.values()) total += frame.pinCount;
    return total;
  }
}
