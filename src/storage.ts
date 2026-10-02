/**
 * Simulated stable storage (disk). Pages carry a pageLSN so REDO can decide
 * idempotently whether a logged update is already reflected on disk.
 */

import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import type { LSN, Page, PageId } from "./types.ts";

interface DiskImage {
  pages: Record<PageId, Page>;
}

export class DiskManager {
  private pages = new Map<PageId, Page>();
  private readonly filePath: string;

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  static create(filePath: string): DiskManager {
    if (existsSync(filePath)) rmSync(filePath);
    const disk = new DiskManager(filePath);
    disk.flush();
    return disk;
  }

  static open(filePath: string): DiskManager {
    const disk = new DiskManager(filePath);
    if (existsSync(filePath)) {
      const image = JSON.parse(readFileSync(filePath, "utf8")) as DiskImage;
      for (const page of Object.values(image.pages)) {
        disk.pages.set(page.pageId, page);
      }
    }
    return disk;
  }

  readPage(pageId: PageId): Page {
    const page = this.pages.get(pageId);
    if (page) return { ...page };
    return { pageId, pageLSN: 0, data: "" };
  }

  /** Apply an update to a page on disk and force it out (steal/no-force model). */
  writePage(pageId: PageId, data: string, pageLSN: LSN): void {
    this.pages.set(pageId, { pageId, pageLSN, data });
    this.flush();
  }

  flush(): void {
    const image: DiskImage = { pages: {} };
    for (const [id, page] of this.pages) image.pages[id] = page;
    writeFileSync(this.filePath, JSON.stringify(image, null, 2), "utf8");
  }

  allPages(): Page[] {
    return [...this.pages.values()].map((p) => ({ ...p }));
  }
}
