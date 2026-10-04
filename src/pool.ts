/**
 * ObjectPool — fixed-overhead object pool for GC pause elimination.
 *
 * Hot-path guarantees:
 *  - acquire()/release() never construct new T instances once warmed up;
 *    objects are recycled from a preallocated free stack.
 *  - No closures, arrays or iterators are created per call.
 *
 * Leak safety (strict mode, used by tests):
 *  - double-release throws
 *  - releasing a foreign object throws
 *  - assertNoLeaks() throws unless every acquired object was released
 */

export interface PoolStats {
  readonly capacity: number;
  readonly created: number;   // total objects ever constructed by the factory
  readonly reused: number;    // acquire() calls served from the free stack
  readonly outstanding: number; // currently checked-out objects
  readonly freeCount: number; // objects sitting on the free stack
  readonly dropped: number;   // released objects discarded because pool was full
}

export class ObjectPool<T extends object> {
  private readonly freeStack: T[] = [];
  private readonly checkedOut: WeakSet<T> = new WeakSet();
  private readonly known: WeakSet<T> = new WeakSet();
  private readonly factory: () => T;
  private readonly reset: (obj: T) => void;
  readonly capacity: number;
  private createdCount = 0;
  private reusedCount = 0;
  private outstandingCount = 0;
  private droppedCount = 0;

  constructor(
    factory: () => T,
    reset: (obj: T) => void,
    capacity: number = 1024,
    preallocate: number = 0,
  ) {
    this.factory = factory;
    this.reset = reset;
    this.capacity = capacity;
    if (capacity < 1) throw new RangeError('pool capacity must be >= 1');
    const n = Math.min(preallocate, capacity);
    for (let i = 0; i < n; i++) {
      const obj = this.factory();
      this.known.add(obj);
      this.createdCount++;
      this.freeStack.push(obj);
    }
  }

  acquire(): T {
    const obj = this.freeStack.pop();
    if (obj === undefined) {
      const fresh = this.factory();
      this.known.add(fresh);
      this.createdCount++;
      this.checkedOut.add(fresh);
      this.outstandingCount++;
      return fresh;
    }
    this.reusedCount++;
    this.checkedOut.add(obj);
    this.outstandingCount++;
    return obj;
  }

  release(obj: T): void {
    if (!this.known.has(obj)) {
      throw new Error('ObjectPool.release: foreign object (not created by this pool)');
    }
    if (!this.checkedOut.has(obj)) {
      throw new Error('ObjectPool.release: double release detected');
    }
    this.checkedOut.delete(obj);
    this.outstandingCount--;
    this.reset(obj);
    if (this.freeStack.length < this.capacity) {
      this.freeStack.push(obj);
    } else {
      this.droppedCount++;
    }
  }

  get stats(): PoolStats {
    return {
      capacity: this.capacity,
      created: this.createdCount,
      reused: this.reusedCount,
      outstanding: this.outstandingCount,
      freeCount: this.freeStack.length,
      dropped: this.droppedCount,
    };
  }

  /** Strict leak assertion — throws if any object is still checked out. */
  assertNoLeaks(context: string = 'ObjectPool'): void {
    if (this.outstandingCount !== 0) {
      throw new Error(`${context}: leak detected, ${this.outstandingCount} object(s) still checked out`);
    }
  }
}
