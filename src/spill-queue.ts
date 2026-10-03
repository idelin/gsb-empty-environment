import { promises as fs } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

export interface SpillQueueOptions {
  /** Max bytes held in memory before overflowing to a temp spill file. */
  maxMemoryBytes: number;
  /**
   * High-water mark for total queued bytes (memory + spilled). When reached,
   * push() blocks until the consumer drains — this is the rate limiter.
   * Defaults to 8x maxMemoryBytes.
   */
  maxQueuedBytes?: number;
  /** Directory for spill files. Defaults to the OS temp dir. */
  spillDir?: string;
}

export interface SpillQueueStats {
  /** Peak bytes simultaneously held in memory. */
  peakMemoryBytes: number;
  /** Total bytes ever written to / read back from the spill file. */
  spillWriteBytes: number;
  spillReadBytes: number;
  /** How many times push() was throttled by the high-water mark. */
  highWaterHits: number;
}

const LENGTH_PREFIX_BYTES = 4;

/**
 * Byte-bounded async FIFO queue with disk spill — the memory-protection
 * rate limiter between producer (chunker) and consumer (receiver).
 *
 * - Items are serialized Buffers, so memory accounting is exact.
 * - When the in-memory budget is exceeded, items overflow to a temp file
 *   instead of growing the heap (prevents receiver-side OOM).
 * - When total queued bytes hit `maxQueuedBytes`, producers are throttled
 *   until the consumer catches up (backpressure / rate limiting).
 */
export class SpillQueue {
  private readonly maxMemoryBytes: number;
  private readonly maxQueuedBytes: number;
  private readonly spillDir: string;

  private mem: Buffer[] = [];
  private memBytes = 0;

  private spillPath: string | null = null;
  private spillWriteOffset = 0;
  private spillReadOffset = 0;
  /** Serializes all spill-file mutations (append / read / reset). */
  private spillLock: Promise<void> = Promise.resolve();

  private closed = false;
  private readonly pushWaiters: Array<() => void> = [];
  private readonly shiftWaiters: Array<() => void> = [];

  private readonly statsData: SpillQueueStats = {
    peakMemoryBytes: 0,
    spillWriteBytes: 0,
    spillReadBytes: 0,
    highWaterHits: 0,
  };

  constructor(options: SpillQueueOptions) {
    if (options.maxMemoryBytes <= 0) {
      throw new RangeError('maxMemoryBytes must be positive');
    }
    this.maxMemoryBytes = options.maxMemoryBytes;
    this.maxQueuedBytes = options.maxQueuedBytes ?? options.maxMemoryBytes * 8;
    this.spillDir = options.spillDir ?? tmpdir();
  }

  get stats(): SpillQueueStats {
    return { ...this.statsData };
  }

  get queuedBytes(): number {
    return this.memBytes + (this.spillWriteOffset - this.spillReadOffset);
  }

  async push(item: Buffer): Promise<void> {
    if (this.closed) throw new Error('push on closed SpillQueue');
    while (this.queuedBytes >= this.maxQueuedBytes) {
      this.statsData.highWaterHits += 1;
      await new Promise<void>((resolve) => this.pushWaiters.push(resolve));
      if (this.closed) throw new Error('push on closed SpillQueue');
    }

    const spillPending = this.spillWriteOffset > this.spillReadOffset;
    if (!spillPending && this.memBytes + item.length <= this.maxMemoryBytes) {
      this.mem.push(item);
      this.memBytes += item.length;
      if (this.memBytes > this.statsData.peakMemoryBytes) {
        this.statsData.peakMemoryBytes = this.memBytes;
      }
    } else {
      await this.withSpillLock(() => this.appendToSpill(item));
    }
    this.notifyShift();
  }

  /** Returns the next item, or undefined once closed and fully drained. */
  async shift(): Promise<Buffer | undefined> {
    for (;;) {
      if (this.mem.length > 0) {
        const item = this.mem.shift() as Buffer;
        this.memBytes -= item.length;
        this.notifyPush();
        return item;
      }
      if (this.spillReadOffset < this.spillWriteOffset) {
        const item = await this.withSpillLock(() => this.readFromSpill());
        this.notifyPush();
        return item;
      }
      if (this.closed) return undefined;
      await new Promise<void>((resolve) => this.shiftWaiters.push(resolve));
    }
  }

  /** No more items will be pushed; shift() drains the remainder. */
  close(): void {
    this.closed = true;
    for (const resolve of this.shiftWaiters.splice(0)) resolve();
    for (const resolve of this.pushWaiters.splice(0)) resolve();
  }

  /** Remove any temp spill file. Safe to call multiple times. */
  async destroy(): Promise<void> {
    await this.withSpillLock(() => this.resetSpill());
  }

  private withSpillLock<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.spillLock.then(fn);
    this.spillLock = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  private async appendToSpill(item: Buffer): Promise<void> {
    if (this.spillPath === null) {
      this.spillPath = join(this.spillDir, `txn-spill-${randomUUID()}.bin`);
    }
    const record = Buffer.allocUnsafe(LENGTH_PREFIX_BYTES + item.length);
    record.writeUInt32LE(item.length, 0);
    item.copy(record, LENGTH_PREFIX_BYTES);
    await fs.appendFile(this.spillPath, record);
    this.spillWriteOffset += record.length;
    this.statsData.spillWriteBytes += record.length;
  }

  private async readFromSpill(): Promise<Buffer> {
    const path = this.spillPath;
    if (path === null) throw new Error('spill read with no spill file');
    const handle = await fs.open(path, 'r');
    try {
      const prefix = Buffer.allocUnsafe(LENGTH_PREFIX_BYTES);
      await handle.read(prefix, 0, LENGTH_PREFIX_BYTES, this.spillReadOffset);
      const length = prefix.readUInt32LE(0);
      const payload = Buffer.allocUnsafe(length);
      await handle.read(payload, 0, length, this.spillReadOffset + LENGTH_PREFIX_BYTES);
      this.spillReadOffset += LENGTH_PREFIX_BYTES + length;
      this.statsData.spillReadBytes += LENGTH_PREFIX_BYTES + length;
      return payload;
    } finally {
      await handle.close();
      // Spill fully drained: reset so new items use the memory fast path.
      if (this.spillReadOffset === this.spillWriteOffset) {
        await this.resetSpill();
      }
    }
  }

  private async resetSpill(): Promise<void> {
    if (this.spillPath !== null) {
      await fs.rm(this.spillPath, { force: true });
      this.spillPath = null;
    }
    this.spillWriteOffset = 0;
    this.spillReadOffset = 0;
  }

  private notifyShift(): void {
    for (const resolve of this.shiftWaiters.splice(0)) resolve();
  }

  private notifyPush(): void {
    for (const resolve of this.pushWaiters.splice(0)) resolve();
  }
}
