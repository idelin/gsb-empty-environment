import type { PageId } from '../log/records.ts';
import { createPage, DiskStorage, type Page } from './disk.ts';

/**
 * 缓冲池：steal + no-force 策略（ARIES 经典配置）。
 * - steal：未提交事务的脏页允许被刷回磁盘；
 * - no-force：提交时不要求脏页落盘，靠 WAL redo 保证持久性。
 * 崩溃 = 直接丢弃缓冲池实例（内存态丢失），仅磁盘与 WAL 留存。
 */
export class BufferPool {
  private readonly disk: DiskStorage;
  private readonly capacity: number;
  private readonly frames = new Map<PageId, { page: Page; dirty: boolean }>();

  constructor(disk: DiskStorage, capacity = 8) {
    this.disk = disk;
    this.capacity = capacity;
  }

  fetchPage(pageId: PageId): Page {
    const hit = this.frames.get(pageId);
    if (hit) return hit.page;
    if (this.frames.size >= this.capacity) this.evictOne();
    const page = this.disk.readPage(pageId) ?? createPage(pageId);
    this.frames.set(pageId, { page, dirty: false });
    return page;
  }

  /** 应用更新并标记脏页；pageLSN 由调用方（日志先行之后）设置 */
  applyUpdate(pageId: PageId, offset: number, value: string, lsn: number): void {
    const page = this.fetchPage(pageId);
    page.data[offset] = value;
    page.pageLSN = lsn;
    this.frames.get(pageId)!.dirty = true;
  }

  markDirty(pageId: PageId): void {
    const frame = this.frames.get(pageId);
    if (frame) frame.dirty = true;
  }

  isDirty(pageId: PageId): boolean {
    return this.frames.get(pageId)?.dirty ?? false;
  }

  getPageLSN(pageId: PageId): number {
    const inMem = this.frames.get(pageId);
    if (inMem) return inMem.page.pageLSN;
    return this.disk.readPage(pageId)?.pageLSN ?? 0;
  }

  /** 恢复引擎专用：直接在缓冲页上重做/补偿 */
  redoApply(pageId: PageId, offset: number, value: string, lsn: number): void {
    this.applyUpdate(pageId, offset, value, lsn);
  }

  private evictOne(): void {
    const [victimId, frame] = this.frames.entries().next().value as [PageId, { page: Page; dirty: boolean }];
    if (frame.dirty) this.disk.writePage(frame.page);
    this.frames.delete(victimId);
  }

  /** 将指定脏页写回磁盘（steal：允许未提交数据落盘） */
  flushPage(pageId: PageId): void {
    const frame = this.frames.get(pageId);
    if (frame && frame.dirty) {
      this.disk.writePage(frame.page);
      frame.dirty = false;
    }
  }

  flushAll(): void {
    for (const [pageId] of this.frames) this.flushPage(pageId);
  }

  dirtyPageIds(): PageId[] {
    return [...this.frames.entries()].filter(([, f]) => f.dirty).map(([id]) => id);
  }
}
