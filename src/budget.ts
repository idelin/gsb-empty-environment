interface Waiter {
  needed: number;
  resolve: () => void;
}

/**
 * Byte-counting semaphore enforcing a hard cap on segment bytes the library
 * retains in memory (queued + in-flight). Segments larger than the limit are
 * never acquired here; callers spill them to disk instead.
 */
export class ByteBudget {
  readonly limit: number;
  private available: number;
  private inUse = 0;
  private highWater = 0;
  private waiters: Waiter[] = [];

  constructor(limit: number) {
    if (!Number.isFinite(limit) || limit <= 0) {
      throw new Error(`budget limit must be a positive number, got ${limit}`);
    }
    this.limit = Math.floor(limit);
    this.available = this.limit;
  }

  get inFlightBytes(): number {
    return this.inUse;
  }

  get highWaterBytes(): number {
    return this.highWater;
  }

  mustSpill(byteCount: number): boolean {
    return byteCount > this.limit;
  }

  async acquire(byteCount: number): Promise<void> {
    if (byteCount <= 0) return;
    if (byteCount > this.limit) {
      throw new Error(`cannot acquire ${byteCount} bytes from a budget of ${this.limit}`);
    }
    if (this.waiters.length === 0 && this.available >= byteCount) {
      this.take(byteCount);
      return;
    }
    await new Promise<void>((resolve) => {
      this.waiters.push({ needed: byteCount, resolve });
    });
  }

  release(byteCount: number): void {
    if (byteCount <= 0) return;
    this.available += byteCount;
    this.inUse -= byteCount;
    while (this.waiters.length > 0) {
      const next = this.waiters[0];
      if (next.needed > this.available) break;
      this.waiters.shift();
      this.take(next.needed);
      next.resolve();
    }
  }

  private take(byteCount: number): void {
    this.available -= byteCount;
    this.inUse += byteCount;
    if (this.inUse > this.highWater) this.highWater = this.inUse;
  }
}
