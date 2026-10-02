import { PAGE_LSN_OFFSET, type DiskManager } from "./disk.ts";
import type { LogManager } from "./log.ts";
import type { LSN, PageId } from "./types.ts";

export class Frame {
  dirty = false;
  pageLSN: LSN;
  readonly pageId: PageId;
  readonly data: Buffer;

  constructor(pageId: PageId, data: Buffer) {
    this.pageId = pageId;
    this.data = data;
    this.pageLSN = Number(data.readBigUInt64LE(PAGE_LSN_OFFSET));
  }
}

export class BufferPool {
  private readonly frames = new Map<PageId, Frame>();
  private readonly disk: DiskManager;
  private readonly log: LogManager;

  constructor(disk: DiskManager, log: LogManager) {
    this.disk = disk;
    this.log = log;
  }

  fetchPage(pageId: PageId): Frame {
    let frame = this.frames.get(pageId);
    if (!frame) {
      frame = new Frame(pageId, this.disk.readPage(pageId));
      this.frames.set(pageId, frame);
    }
    return frame;
  }

  flushPage(pageId: PageId): void {
    const frame = this.frames.get(pageId);
    if (!frame || !frame.dirty) return;
    this.log.flush(frame.pageLSN);
    frame.data.writeBigUInt64LE(BigInt(frame.pageLSN), PAGE_LSN_OFFSET);
    this.disk.writePage(frame.pageId, frame.data);
    frame.dirty = false;
  }

  flushAll(): void {
    for (const pageId of this.frames.keys()) this.flushPage(pageId);
    this.disk.sync();
  }

  dirtyPageIds(): PageId[] {
    const out: PageId[] = [];
    for (const frame of this.frames.values()) {
      if (frame.dirty) out.push(frame.pageId);
    }
    return out;
  }
}
