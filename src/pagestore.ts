/**
 * File-backed page store and a pin-counted buffer pool.
 *
 * Page layout (PAGE_SIZE bytes): u64 pageLSN header, then raw data bytes.
 * The buffer pool tracks pins so tests can assert strict absence of leaks:
 * every pin must be matched by exactly one unpin.
 */

import { open, type FileHandle } from "node:fs/promises";

export const PAGE_SIZE = 4096;
export const PAGE_LSN_OFFSET = 0;
export const PAGE_DATA_OFFSET = 8;

export function getPageLSN(page: Buffer): number {
  return Number(page.readBigUInt64LE(PAGE_LSN_OFFSET));
}

export function setPageLSN(page: Buffer, lsn: number): void {
  page.writeBigUInt64LE(BigInt(lsn), PAGE_LSN_OFFSET);
}

export class PageStore {
  private fh: FileHandle | null = null;
  readonly path: string;
  readonly pageSize: number;

  private constructor(path: string, pageSize: number) {
    this.path = path;
    this.pageSize = pageSize;
  }

  static async create(path: string, pageSize = PAGE_SIZE): Promise<PageStore> {
    const store = new PageStore(path, pageSize);
    store.fh = await open(path, "w+");
    return store;
  }

  static async open(path: string, pageSize = PAGE_SIZE): Promise<PageStore> {
    const store = new PageStore(path, pageSize);
    store.fh = await open(path, "r+");
    return store;
  }

  get closed(): boolean {
    return this.fh === null;
  }

  private handle(): FileHandle {
    if (!this.fh) throw new Error("PageStore is closed");
    return this.fh;
  }

  /** Read a page; regions beyond EOF read as zeros (never-allocated page). */
  async readPageRaw(pageId: number): Promise<Buffer> {
    const buf = Buffer.alloc(this.pageSize);
    const { bytesRead } = await this.handle().read(
      buf,
      0,
      this.pageSize,
      pageId * this.pageSize,
    );
    if (bytesRead < this.pageSize) buf.fill(0, bytesRead);
    return buf;
  }

  async writePageRaw(pageId: number, buf: Buffer): Promise<void> {
    if (buf.length !== this.pageSize) {
      throw new Error(`page buffer must be ${this.pageSize} bytes`);
    }
    await this.handle().write(buf, 0, this.pageSize, pageId * this.pageSize);
  }

  async sync(): Promise<void> {
    await this.handle().sync();
  }

  async close(): Promise<void> {
    if (this.fh) {
      await this.fh.close();
      this.fh = null;
    }
  }
}

interface Frame {
  pageId: number;
  buf: Buffer | null;
  /** Resolves once an in-flight disk read populates buf. */
  loading: Promise<void> | null;
  dirty: boolean;
  recLSN: number;
  pins: number;
}

export class BufferPool {
  private frames = new Map<number, Frame>();
  private allocCount = 0;
  readonly store: PageStore;

  constructor(store: PageStore) {
    this.store = store;
  }

  async pin(pageId: number): Promise<Buffer> {
    let frame = this.frames.get(pageId);
    if (!frame) {
      // Register the frame synchronously so concurrent pins of the same page
      // share a single load instead of racing to allocate duplicate frames.
      frame = { pageId, buf: null, loading: null, dirty: false, recLSN: 0, pins: 0 };
      this.frames.set(pageId, frame);
      this.allocCount++;
      frame.loading = this.store.readPageRaw(pageId).then((buf) => {
        frame!.buf = buf;
      });
    }
    if (frame.loading) {
      await frame.loading;
      frame.loading = null;
    }
    frame.pins++;
    return frame.buf as Buffer;
  }

  unpin(pageId: number, opts: { dirty?: boolean; recLSN?: number } = {}): void {
    const frame = this.frames.get(pageId);
    if (!frame || frame.pins <= 0) {
      throw new Error(`unpin of unpinned page ${pageId}`);
    }
    frame.pins--;
    if (opts.dirty) {
      if (!frame.dirty) frame.recLSN = opts.recLSN ?? 0;
      frame.dirty = true;
    }
  }

  /** Dirty-page table snapshot: pageId -> recLSN (oldest unflushed update). */
  dirtyPages(): { pageId: number; recLSN: number }[] {
    const out: { pageId: number; recLSN: number }[] = [];
    for (const f of this.frames.values()) {
      if (f.dirty) out.push({ pageId: f.pageId, recLSN: f.recLSN });
    }
    return out;
  }

  async flushPage(pageId: number): Promise<void> {
    const frame = this.frames.get(pageId);
    if (frame && frame.dirty && frame.buf) {
      await this.store.writePageRaw(pageId, frame.buf as Buffer);
      frame.dirty = false;
      frame.recLSN = 0;
    }
  }

  async flushAll(): Promise<void> {
    for (const f of this.frames.values()) {
      if (f.loading) await f.loading;
      if (f.dirty && f.buf) {
        await this.store.writePageRaw(f.pageId, f.buf as Buffer);
        f.dirty = false;
        f.recLSN = 0;
      }
    }
    await this.store.sync();
  }

  /** Simulate a crash: drop all buffered state without flushing. */
  discardAll(): void {
    this.frames.clear();
  }

  /** Throw if any frame is still pinned (leak detection). */
  assertNoLeaks(): void {
    for (const f of this.frames.values()) {
      if (f.pins !== 0) {
        throw new Error(`page ${f.pageId} leaked: ${f.pins} outstanding pin(s)`);
      }
    }
  }

  get stats(): { frames: number; allocations: number; pinned: number } {
    let pinned = 0;
    for (const f of this.frames.values()) if (f.pins > 0) pinned++;
    return { frames: this.frames.size, allocations: this.allocCount, pinned };
  }
}
