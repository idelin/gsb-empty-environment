import type { JsonValue, LSN, PageId } from './types.ts';
import type { Storage } from './storage.ts';
import type { Wal } from './wal.ts';

/** 缓冲池中的页帧。dirty=true 表示页被修改但尚未写回磁盘。 */
export interface Page {
  pageId: PageId;
  pageLSN: LSN;
  dirty: boolean;
  data: Record<string, JsonValue>;
}

/**
 * 缓冲池（Buffer Pool），采用 STEAL + NO-FORCE 策略：
 * - STEAL：未提交事务修改的脏页允许被刷回磁盘（恢复时靠 Undo 清理）；
 * - NO-FORCE：事务提交时不要求刷页（恢复时靠 Redo 重放）。
 */
export class BufferPool {
  private pages = new Map<PageId, Page>();
  private storage: Storage;
  private wal: Wal;

  constructor(storage: Storage, wal: Wal) {
    this.storage = storage;
    this.wal = wal;
  }

  fetch(pageId: PageId): Page {
    let page = this.pages.get(pageId);
    if (!page) {
      const image = this.storage.readPage(pageId);
      page = {
        pageId,
        pageLSN: image?.pageLSN ?? 0,
        dirty: false,
        data: image?.data ?? {},
      };
      this.pages.set(pageId, page);
    }
    return page;
  }

  /** 应用一次修改（value 为 undefined 表示删除该键），并更新 pageLSN。 */
  apply(pageId: PageId, key: string, value: JsonValue | undefined, lsn: LSN): void {
    const page = this.fetch(pageId);
    if (value === undefined) delete page.data[key];
    else page.data[key] = value;
    page.pageLSN = lsn;
    page.dirty = true;
  }

  read(pageId: PageId, key: string): JsonValue | undefined {
    return this.fetch(pageId).data[key];
  }

  flushPage(pageId: PageId): void {
    const page = this.pages.get(pageId);
    if (!page || !page.dirty) return;
    // WAL 协议核心：刷脏页之前，先把 pageLSN 之前的日志强制落盘。
    this.wal.flush(page.pageLSN);
    this.storage.writePage(pageId, { pageLSN: page.pageLSN, data: page.data });
    page.dirty = false;
  }

  flushAll(): void {
    for (const pageId of [...this.pages.keys()]) this.flushPage(pageId);
  }

  dirtyPageIds(): PageId[] {
    return [...this.pages.values()].filter((p) => p.dirty).map((p) => p.pageId);
  }

  /** 模拟崩溃：缓冲池内容全部丢失。 */
  discard(): void {
    this.pages.clear();
  }
}
