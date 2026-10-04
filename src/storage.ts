/**
 * Page-oriented storage: a file-backed page store plus a buffer pool that
 * tracks pageLSN (the LSN of the most recent log record applied to the
 * page). ARIES redo/undo decisions compare log LSNs against pageLSN.
 */

import * as fs from "fs";
import { CodecError } from "./codec";

export const PAGE_SIZE = 4096;
export const PAGE_LSN_OFFSET = 0;
export const PAGE_HEADER_SIZE = 8; // u64 pageLSN

export class PageStore {
  private fd: number;
  private closed = false;

  constructor(public readonly path: string) {
    this.fd = fs.openSync(path, "a+");
  }

  get isClosed(): boolean {
    return this.closed;
  }

  get pageCount(): number {
    return Math.ceil(fs.fstatSync(this.fd).size / PAGE_SIZE);
  }

  /** Returns a fresh copy of the page; missing pages read as zero-filled. */
  readPage(pageId: number): Buffer {
    this.assertOpen();
    const page = Buffer.alloc(PAGE_SIZE);
    const offset = pageId * PAGE_SIZE;
    if (offset < fs.fstatSync(this.fd).size) {
      fs.readSync(this.fd, page, 0, PAGE_SIZE, offset);
    }
    return page;
  }

  writePage(pageId: number, page: Buffer): void {
    this.assertOpen();
    if (page.length !== PAGE_SIZE) {
      throw new CodecError(`page must be ${PAGE_SIZE} bytes`, page.length);
    }
    fs.writeSync(this.fd, page, 0, PAGE_SIZE, pageId * PAGE_SIZE);
    fs.fsyncSync(this.fd);
  }

  private assertOpen(): void {
    if (this.closed) throw new Error("PageStore: already closed");
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    fs.closeSync(this.fd);
  }
}

export interface Frame {
  pageId: number;
  page: Buffer;
  pageLSN: number;
  dirty: boolean;
}

export class BufferPool {
  private frames = new Map<number, Frame>();

  constructor(
    private readonly store: PageStore,
    private readonly capacity = 64,
  ) {
    if (capacity <= 0) throw new Error("BufferPool: capacity must be positive");
  }

  get size(): number {
    return this.frames.size;
  }

  get dirtyPageIds(): number[] {
    const ids: number[] = [];
    for (const f of this.frames.values()) if (f.dirty) ids.push(f.pageId);
    return ids;
  }

  get(pageId: number): Frame {
    let frame = this.frames.get(pageId);
    if (frame) return frame;
    if (this.frames.size >= this.capacity) this.evictOne();
    const page = this.store.readPage(pageId);
    frame = {
      pageId,
      page,
      pageLSN: Number(page.readBigUInt64LE(PAGE_LSN_OFFSET)),
      dirty: false,
    };
    this.frames.set(pageId, frame);
    return frame;
  }

  /** Steal eviction: a dirty victim is flushed before being dropped. */
  private evictOne(): void {
    const first = this.frames.keys().next();
    if (first.done) return;
    const victim = this.frames.get(first.value)!;
    if (victim.dirty) this.flushPage(victim.pageId);
    this.frames.delete(first.value);
  }

  private checkBounds(offset: number, length: number): void {
    if (
      !Number.isInteger(offset) ||
      offset < PAGE_HEADER_SIZE ||
      offset + length > PAGE_SIZE
    ) {
      throw new CodecError(
        `page write out of bounds (offset ${offset}, len ${length})`,
        offset,
      );
    }
  }

  /** Applies `data` at `offset` and stamps the frame with `lsn`. */
  applyBytes(pageId: number, offset: number, data: Buffer, lsn: number): void {
    this.checkBounds(offset, data.length);
    const frame = this.get(pageId);
    frame.page.set(data, offset);
    frame.pageLSN = lsn;
    frame.dirty = true;
  }

  /** Returns a copy of page bytes in [offset, offset+length). */
  readBytes(pageId: number, offset: number, length: number): Buffer {
    this.checkBounds(offset, length);
    const frame = this.get(pageId);
    const out = Buffer.allocUnsafe(length);
    frame.page.copy(out, 0, offset, offset + length);
    return out;
  }

  pageLSN(pageId: number): number {
    return this.get(pageId).pageLSN;
  }

  flushPage(pageId: number): void {
    const frame = this.frames.get(pageId);
    if (!frame || !frame.dirty) return;
    frame.page.writeBigUInt64LE(BigInt(frame.pageLSN), PAGE_LSN_OFFSET);
    this.store.writePage(pageId, frame.page);
    frame.dirty = false;
  }

  flushAll(): void {
    for (const frame of this.frames.values()) {
      if (frame.dirty) this.flushPage(frame.pageId);
    }
  }

  /** Crash simulation: drop every frame without flushing. */
  discardAll(): void {
    this.frames.clear();
  }

  /** Releases all frames (flushing nothing); used at shutdown checks. */
  close(): void {
    this.frames.clear();
  }
}
