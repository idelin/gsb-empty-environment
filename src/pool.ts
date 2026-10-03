/**
 * Object pool for GC-pressure elimination on hot decode paths.
 *
 * Safety guarantees:
 * - double release is detected and throws;
 * - outstanding accounting powers leak assertions (assertNoLeaks);
 * - capacity bound: objects released beyond capacity are dropped for GC,
 *   keeping worst-case retained memory bounded.
 */
export class ObjectPool<T> {
  private readonly free: T[] = [];
  private readonly pooled = new Set<T>();
  private created = 0;
  private reused = 0;
  private dropped = 0;
  private outstandingCount = 0;
  private readonly factory: () => T;
  private readonly resetFn: (obj: T) => void;
  readonly capacity: number;

  constructor(factory: () => T, resetFn: (obj: T) => void, capacity = 1024) {
    if (capacity < 0) throw new Error('pool capacity must be >= 0');
    this.factory = factory;
    this.resetFn = resetFn;
    this.capacity = capacity;
  }

  acquire(): T {
    const obj = this.free.pop();
    this.outstandingCount++;
    if (obj === undefined) {
      this.created++;
      return this.factory();
    }
    this.pooled.delete(obj);
    this.reused++;
    return obj;
  }

  release(obj: T): void {
    if (this.pooled.has(obj)) {
      throw new Error('double release detected: object is already in the pool');
    }
    this.outstandingCount--;
    if (this.outstandingCount < 0) {
      throw new Error('release without matching acquire');
    }
    if (this.free.length < this.capacity) {
      this.resetFn(obj);
      this.free.push(obj);
      this.pooled.add(obj);
    } else {
      this.dropped++;
    }
  }

  get size(): number {
    return this.free.length;
  }

  get outstanding(): number {
    return this.outstandingCount;
  }

  get stats(): { created: number; reused: number; dropped: number; outstanding: number; pooled: number } {
    return {
      created: this.created,
      reused: this.reused,
      dropped: this.dropped,
      outstanding: this.outstandingCount,
      pooled: this.free.length,
    };
  }

  assertNoLeaks(context = 'pool'): void {
    if (this.outstandingCount !== 0) {
      throw new Error(`${context}: leak detected, ${this.outstandingCount} object(s) never released`);
    }
  }
}
