import type { DirtyPageEntry, LSN, PageId } from './types.ts';
import { NULL_LSN } from './types.ts';

export interface PageImage {
  data: number;
  pageLSN: LSN;
}

/** 模拟稳定存储（磁盘）：pageId -> { data, pageLSN } */
export class DiskStorage {
  private readonly pages = new Map<PageId, PageImage>();

  constructor(initial: Record<string, number> = {}) {
    for (const [pageId, value] of Object.entries(initial)) {
      this.pages.set(pageId, { data: value, pageLSN: NULL_LSN });
    }
  }

  hasPage(pageId: PageId): boolean {
    return this.pages.has(pageId);
  }

  readPage(pageId: PageId): PageImage | undefined {
    const page = this.pages.get(pageId);
    return page === undefined ? undefined : { ...page };
  }

  writePage(pageId: PageId, data: number, pageLSN: LSN): void {
    this.pages.set(pageId, { data, pageLSN });
  }

  pageIds(): PageId[] {
    return [...this.pages.keys()];
  }

  snapshot(): Map<PageId, PageImage> {
    return new Map([...this.pages.entries()].map(([k, v]) => [k, { ...v }]));
  }
}

interface Frame {
  data: number;
  pageLSN: LSN;
  dirty: boolean;
  recLSN: LSN;
}

/**
 * 缓冲池：跟踪每页 pageLSN 与脏页 recLSN（首次弄脏该页的日志 LSN），
 * 崩溃时直接丢弃整个实例即可模拟易失性。
 */
export class BufferPool {
  private readonly frames = new Map<PageId, Frame>();

  private readonly disk: DiskStorage;

  constructor(disk: DiskStorage) {
    this.disk = disk;
  }

  get(pageId: PageId): PageImage {
    let frame = this.frames.get(pageId);
    if (frame === undefined) {
      const page = this.disk.readPage(pageId);
      if (page === undefined) throw new Error(`缓冲池读取了不存在的页面: ${pageId}`);
      frame = { data: page.data, pageLSN: page.pageLSN, dirty: false, recLSN: NULL_LSN };
      this.frames.set(pageId, frame);
    }
    return { data: frame.data, pageLSN: frame.pageLSN };
  }

  applyUpdate(pageId: PageId, value: number, lsn: LSN): void {
    this.get(pageId);
    const frame = this.frames.get(pageId)!;
    if (!frame.dirty) {
      frame.dirty = true;
      frame.recLSN = lsn;
    }
    frame.data = value;
    frame.pageLSN = lsn;
  }

  flushPage(pageId: PageId): void {
    const frame = this.frames.get(pageId);
    if (frame === undefined || !frame.dirty) return;
    this.disk.writePage(pageId, frame.data, frame.pageLSN);
    frame.dirty = false;
    frame.recLSN = NULL_LSN;
  }

  flushAll(): void {
    for (const pageId of this.frames.keys()) this.flushPage(pageId);
  }

  dirtyPageTable(): DirtyPageEntry[] {
    const entries: DirtyPageEntry[] = [];
    for (const [pageId, frame] of this.frames) {
      if (frame.dirty) entries.push({ pageId, recLSN: frame.recLSN });
    }
    return entries;
  }

  dirtyCount(): number {
    return this.dirtyPageTable().length;
  }
}
