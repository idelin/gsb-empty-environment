import { EngineError, ErrorCodes } from "../errors.ts";
import type { MetricsRegistry } from "../metrics/registry.ts";
import type { DiskManager } from "./disk.ts";

export interface Frame {
  pageId: number;
  data: string;
  pageLSN: number;
  dirty: boolean;
  pinCount: number;
  /** LSN of the first update that dirtied the frame since last flush. */
  recLSN: number;
}

/**
 * Buffer pool over DiskManager. Tracks hit/miss counters (hit rate is derived
 * at export time) and dirty-page bookkeeping used to build the checkpoint DPT.
 */
export class BufferPool {
  private readonly frames = new Map<number, Frame>();
  private readonly disk: DiskManager;
  private readonly metrics: MetricsRegistry;
  readonly capacity: number;

  constructor(disk: DiskManager, metrics: MetricsRegistry, capacity = 128) {
    this.disk = disk;
    this.metrics = metrics;
    this.capacity = capacity;
  }

  /** Fetches a page into the pool (pin count +1). */
  getPage(pageId: number): Frame {
    let frame = this.frames.get(pageId);
    if (frame !== undefined) {
      this.metrics.counter("buffer.hit").inc();
      frame.pinCount++;
      return frame;
    }
    this.metrics.counter("buffer.miss").inc();
    if (this.frames.size >= this.capacity) {
      this.evictOne();
    }
    const page = this.disk.readPage(pageId);
    frame = { pageId, data: page.data, pageLSN: page.pageLSN, dirty: false, pinCount: 1, recLSN: 0 };
    this.frames.set(pageId, frame);
    return frame;
  }

  private evictOne(): void {
    for (const frame of this.frames.values()) {
      if (frame.pinCount === 0) {
        if (frame.dirty) this.disk.writePage(frame.pageId, frame);
        this.frames.delete(frame.pageId);
        return;
      }
    }
    throw new EngineError(ErrorCodes.BUFFER_PIN_VIOLATION, "all buffer frames are pinned");
  }

  unpin(pageId: number): void {
    const frame = this.frames.get(pageId);
    if (frame === undefined) {
      throw new EngineError(ErrorCodes.BUFFER_PAGE_NOT_FOUND, `unpin of unknown page ${pageId}`);
    }
    if (frame.pinCount <= 0) {
      throw new EngineError(ErrorCodes.BUFFER_PIN_VIOLATION, `page ${pageId} pin count underflow`);
    }
    frame.pinCount--;
  }

  /** Applies an update in memory and stamps the frame with the log LSN. */
  updatePage(pageId: number, data: string, lsn: number): void {
    const frame = this.getPage(pageId);
    if (!frame.dirty) frame.recLSN = lsn;
    frame.data = data;
    frame.pageLSN = lsn;
    frame.dirty = true;
    this.unpin(pageId);
  }

  flushPage(pageId: number): void {
    const frame = this.frames.get(pageId);
    if (frame === undefined) return; // not cached: already on disk
    this.disk.writePage(pageId, frame);
    frame.dirty = false;
    frame.recLSN = 0;
    this.metrics.counter("buffer.flushes").inc();
  }

  flushAll(): void {
    for (const frame of this.frames.values()) {
      if (frame.dirty) {
        this.disk.writePage(frame.pageId, frame);
        frame.dirty = false;
        frame.recLSN = 0;
        this.metrics.counter("buffer.flushes").inc();
      }
    }
  }

  /** pageId -> recLSN for every dirty frame (input to checkpoint DPT). */
  dirtyPages(): Map<number, number> {
    const out = new Map<number, number>();
    for (const frame of this.frames.values()) {
      if (frame.dirty) out.set(frame.pageId, frame.recLSN);
    }
    return out;
  }

  /** No-leak invariant: every frame unpinned and clean. */
  assertQuiescent(): void {
    for (const frame of this.frames.values()) {
      if (frame.pinCount !== 0) {
        throw new EngineError(ErrorCodes.BUFFER_PIN_VIOLATION, `page ${frame.pageId} still pinned`);
      }
      if (frame.dirty) {
        throw new EngineError(ErrorCodes.BUFFER_PIN_VIOLATION, `page ${frame.pageId} still dirty`);
      }
    }
  }

  get size(): number {
    return this.frames.size;
  }
}
