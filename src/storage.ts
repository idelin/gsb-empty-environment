import { existsSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { NULL_LSN, type LSN, type PageId, type PageValue } from "./log-record.ts";

export interface Page {
  pageId: PageId;
  data: PageValue;
  pageLSN: LSN;
  dirty: boolean;
}

interface DiskImage {
  pages: Record<PageId, { data: PageValue; pageLSN: LSN }>;
}

/**
 * Durable page storage backed by a JSON file. Writes are atomic
 * (write tmp + rename) so a "crash" never sees a torn page.
 */
export class DiskStorage {
  private pages = new Map<PageId, { data: PageValue; pageLSN: LSN }>();
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
    if (existsSync(filePath)) {
      const raw = readFileSync(filePath, "utf8").trim();
      if (raw.length > 0) {
        const image = JSON.parse(raw) as DiskImage;
        for (const [pageId, page] of Object.entries(image.pages)) {
          this.pages.set(pageId, { data: page.data, pageLSN: page.pageLSN });
        }
      }
    }
  }

  readPage(pageId: PageId): { data: PageValue; pageLSN: LSN } | undefined {
    const page = this.pages.get(pageId);
    return page ? { data: page.data, pageLSN: page.pageLSN } : undefined;
  }

  writePage(pageId: PageId, data: PageValue, pageLSN: LSN): void {
    this.pages.set(pageId, { data, pageLSN });
    this.persist();
  }

  private persist(): void {
    const image: DiskImage = { pages: Object.fromEntries(this.pages) };
    const tmp = `${this.filePath}.tmp`;
    writeFileSync(tmp, JSON.stringify(image, null, 2), "utf8");
    renameSync(tmp, this.filePath);
  }
}

/**
 * Buffer pool over DiskStorage. Pages carry a pageLSN used by Redo to decide
 * whether a logged update is already reflected on the page (idempotency).
 * A crash is modeled by discarding the pool: unflushed dirty pages vanish.
 */
export class BufferPool {
  private pages = new Map<PageId, Page>();
  private flushCount = 0;
  private readonly disk: DiskStorage;

  constructor(disk: DiskStorage) {
    this.disk = disk;
  }

  fetchPage(pageId: PageId): Page {
    let page = this.pages.get(pageId);
    if (!page) {
      const onDisk = this.disk.readPage(pageId);
      page = {
        pageId,
        data: onDisk ? onDisk.data : null,
        pageLSN: onDisk ? onDisk.pageLSN : NULL_LSN,
        dirty: false,
      };
      this.pages.set(pageId, page);
    }
    return page;
  }

  /** Apply a logged change to a page, tagging it with the record's LSN. */
  applyLogged(pageId: PageId, value: PageValue, lsn: LSN): Page {
    const page = this.fetchPage(pageId);
    page.data = value;
    page.pageLSN = lsn;
    page.dirty = true;
    return page;
  }

  flushPage(pageId: PageId): void {
    const page = this.pages.get(pageId);
    if (!page || !page.dirty) return;
    this.disk.writePage(page.pageId, page.data, page.pageLSN);
    page.dirty = false;
    this.flushCount += 1;
  }

  flushAll(): void {
    for (const page of this.pages.values()) {
      if (page.dirty) this.flushPage(page.pageId);
    }
  }

  dirtyPageIds(): PageId[] {
    return [...this.pages.values()].filter((p) => p.dirty).map((p) => p.pageId);
  }

  get totalFlushes(): number {
    return this.flushCount;
  }
}
