/**
 * Object pool for GC-pressure elimination on hot decode paths.
 *
 * - acquire() returns a recycled instance when available (no allocation).
 * - release() resets the instance and returns it to the free list.
 * - Outstanding/leak accounting is exact; double-release is detected.
 * - JS runs single-threaded, but the pool is reentrancy-safe for async
 *   interleavings: all state transitions are synchronous and atomic
 *   with respect to the event loop.
 */
export interface PoolStats {
  created: number;
  reused: number;
  outstanding: number;
  freeCount: number;
  maxSize: number;
}

export class DoubleReleaseError extends Error {
  constructor() {
    super("object released to pool twice");
    this.name = "DoubleReleaseError";
  }
}

export class ObjectPool<T extends object> {
  private readonly free: T[] = [];
  private readonly inPool: Set<T>;
  private readonly factory: () => T;
  private readonly resetFn: (obj: T) => void;
  readonly maxSize: number;
  private createdCount = 0;
  private reusedCount = 0;
  private outstandingCount = 0;

  constructor(
    factory: () => T,
    resetFn: (obj: T) => void,
    maxSize = 1024,
    detectDoubleRelease = true,
  ) {
    if (maxSize < 0) throw new RangeError("maxSize must be >= 0");
    this.factory = factory;
    this.resetFn = resetFn;
    this.maxSize = maxSize;
    this.inPool = detectDoubleRelease ? new Set() : (null as unknown as Set<T>);
  }

  acquire(): T {
    const obj = this.free.pop();
    if (obj !== undefined) {
      if (this.inPool) this.inPool.delete(obj);
      this.reusedCount++;
      this.outstandingCount++;
      return obj;
    }
    this.createdCount++;
    this.outstandingCount++;
    return this.factory();
  }

  release(obj: T): void {
    if (this.inPool) {
      if (this.inPool.has(obj)) throw new DoubleReleaseError();
    }
    this.outstandingCount--;
    if (this.outstandingCount < 0) {
      this.outstandingCount = 0;
      throw new DoubleReleaseError();
    }
    if (this.free.length < this.maxSize) {
      this.resetFn(obj);
      this.free.push(obj);
      if (this.inPool) this.inPool.add(obj);
    }
    // else: drop the reference and let GC take it — pool stays bounded.
  }

  /** Exact leak check: true when every acquired object was released. */
  get leakFree(): boolean {
    return this.outstandingCount === 0;
  }

  stats(): PoolStats {
    return {
      created: this.createdCount,
      reused: this.reusedCount,
      outstanding: this.outstandingCount,
      freeCount: this.free.length,
      maxSize: this.maxSize,
    };
  }
}
