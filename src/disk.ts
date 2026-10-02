import {
  closeSync,
  existsSync,
  fsyncSync,
  openSync,
  readSync,
  statSync,
  writeSync,
} from "node:fs";
import type { PageId } from "./types.ts";

export const PAGE_SIZE = 4096;
export const PAGE_LSN_OFFSET = 0;
export const PAGE_HEADER_SIZE = 8;
export const PAGE_DATA_SIZE = PAGE_SIZE - PAGE_HEADER_SIZE;

export class DiskManager {
  private readonly fd: number;
  private numPages: number;
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
    this.fd = openSync(file, existsSync(file) ? "r+" : "w+");
    this.numPages = Math.floor(statSync(file).size / PAGE_SIZE);
  }

  get pageCount(): number {
    return this.numPages;
  }

  allocatePage(): PageId {
    const pageId = this.numPages++;
    writeSync(this.fd, Buffer.alloc(PAGE_SIZE), 0, PAGE_SIZE, pageId * PAGE_SIZE);
    return pageId;
  }

  readPage(pageId: PageId): Buffer {
    const buf = Buffer.alloc(PAGE_SIZE);
    readSync(this.fd, buf, 0, PAGE_SIZE, pageId * PAGE_SIZE);
    return buf;
  }

  writePage(pageId: PageId, data: Buffer): void {
    if (data.length !== PAGE_SIZE) {
      throw new Error(`page write must be exactly ${PAGE_SIZE} bytes`);
    }
    writeSync(this.fd, data, 0, PAGE_SIZE, pageId * PAGE_SIZE);
  }

  sync(): void {
    fsyncSync(this.fd);
  }

  close(): void {
    closeSync(this.fd);
  }
}
