/**
 * 页存储与缓冲池。
 * DiskStorage 模拟持久化磁盘；BufferPool 模拟易失内存（崩溃即丢失）。
 * 每页维护 pageLSN：最近一次作用于该页的日志 LSN，Redo 阶段据此跳过已落盘修改。
 */
import type { LSN, PageId } from "./logRecord.ts";

export interface Page {
  pageId: PageId;
  data: string;
  pageLSN: LSN;
}

export class DiskStorage {
  private pages = new Map<PageId, Page>();

  readPage(pageId: PageId): Page {
    const page = this.pages.get(pageId);
    if (!page) return { pageId, data: "", pageLSN: 0 };
    return { ...page };
  }

  writePage(page: Page): void {
    this.pages.set(page.pageId, { ...page });
  }

  allPages(): Page[] {
    return [...this.pages.values()].map((p) => ({ ...p }));
  }
}

export class BufferPool {
  private frames = new Map<PageId, Page>();
  private dirty = new Set<PageId>();
  private readonly disk: DiskStorage;

  constructor(disk: DiskStorage) {
    this.disk = disk;
  }

  fetch(pageId: PageId): Page {
    let page = this.frames.get(pageId);
    if (!page) {
      page = this.disk.readPage(pageId);
      this.frames.set(pageId, page);
    }
    return page;
  }

  /** 在缓冲中更新页并标记脏页（不写盘） */
  update(pageId: PageId, data: string, lsn: LSN): void {
    const page = this.fetch(pageId);
    page.data = data;
    page.pageLSN = lsn;
    this.dirty.add(pageId);
  }

  /** 崩溃：缓冲池整体丢失 */
  crash(): void {
    this.frames.clear();
    this.dirty.clear();
  }

  /** 正常 shutdown 前刷脏页 */
  flushAll(): void {
    for (const pageId of this.dirty) {
      this.disk.writePage(this.frames.get(pageId)!);
    }
    this.dirty.clear();
  }

  isDirty(pageId: PageId): boolean {
    return this.dirty.has(pageId);
  }
}
