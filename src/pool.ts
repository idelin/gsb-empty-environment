/**
 * ObjectPool<T> — fixed-overhead object recycling for hot paths.
 *
 * Design goals:
 *  - acquire()/release() on the freelist perform zero heap allocations
 *    (Array.pop / Array.push on a pre-sized backing store only).
 *  - Strict leak & misuse detection: double-release, releasing foreign
 *    objects, and use-count drift all throw immediately.
 *  - Observable: counters let tests assert "no new allocations after
 *    warm-up" (totalCreated stays flat) and "no leaks" (outstanding === 0).
 */

export interface PoolOptions<T> {
  /** Creates a fresh instance. Only called when the freelist is empty. */
  factory: () => T;
  /** Returns an instance to a pristine state before it is cached. */
  reset: (obj: T) => void;
  /** Maximum number of idle instances kept in the freelist. */
  capacity: number;
  /** Instances created eagerly at construction. Defaults to capacity. */
  prealloc?: number;
  /**
   * Hard cap on simultaneously checked-out objects. acquire() throws when
   * exceeded, turning unbounded growth into a loud failure. Default: none.
   */
  maxOutstanding?: number;
  /** Optional name for diagnostics. */
  name?: string;
}

export class ObjectPool<T> {
  readonly name: string;
  readonly capacity: number;
  private readonly factory: () => T;
  private readonly resetFn: (obj: T) => void;
  private readonly maxOutstanding: number;

  private readonly freelist: T[] = [];
  /** Live checked-out objects — enables double-release / foreign-release detection. */
  private readonly checkedOut: Set<T> = new Set();

  // Observability counters (monotonic).
  totalCreated = 0;
  totalAcquires = 0;
  totalReleases = 0;
  totalReused = 0;

  constructor(opts: PoolOptions<T>) {
    if (!Number.isInteger(opts.capacity) || opts.capacity <= 0) {
      throw new RangeError("capacity must be a positive integer");
    }
    this.factory = opts.factory;
    this.resetFn = opts.reset;
    this.capacity = opts.capacity;
    this.maxOutstanding = opts.maxOutstanding ?? Number.POSITIVE_INFINITY;
    this.name = opts.name ?? "pool";

    const prealloc = opts.prealloc ?? opts.capacity;
    if (prealloc > opts.capacity) {
      throw new RangeError("prealloc cannot exceed capacity");
    }
    for (let i = 0; i < prealloc; i++) {
      this.freelist.push(this.factory());
      this.totalCreated++;
    }
  }

  /** Number of objects currently checked out (leak detector: must return to 0). */
  get outstanding(): number {
    return this.checkedOut.size;
  }

  get idleCount(): number {
    return this.freelist.length;
  }

  /**
   * Checkout an instance. Hot path: freelist pop, zero allocation.
   * Throws if maxOutstanding would be exceeded.
   */
  acquire(): T {
    this.totalAcquires++;
    if (this.checkedOut.size >= this.maxOutstanding) {
      throw new Error(
        `pool[${this.name}] exhausted: ${this.checkedOut.size} outstanding (max ${this.maxOutstanding})`
      );
    }
    let obj: T;
    if (this.freelist.length > 0) {
      obj = this.freelist.pop() as T;
      this.totalReused++;
    } else {
      obj = this.factory();
      this.totalCreated++;
    }
    this.checkedOut.add(obj);
    return obj;
  }
  /**
   * Return an instance. The object is reset BEFORE being cached, so stale
   * state can never leak into the next acquire(). Double-release and
   * releasing objects this pool never created both throw.
   */
  release(obj: T): void {
    if (!this.checkedOut.has(obj)) {
      throw new Error(`pool[${this.name}] release of unknown or already-released object`);
    }
    this.checkedOut.delete(obj);
    this.totalReleases++;
    this.resetFn(obj);
    if (this.freelist.length < this.capacity) {
      this.freelist.push(obj);
    }
    // else: freelist full — object becomes garbage by design (bounded).
  }

  /**
   * Release every checked-out object. Test teardown helper that converts
   * "leak" into an explicit, auditable bulk return.
   */
  releaseAll(): void {
    for (const obj of this.checkedOut) {
      this.totalReleases++;
      this.resetFn(obj);
      if (this.freelist.length < this.capacity) this.freelist.push(obj);
    }
    this.checkedOut.clear();
  }

  /** Drop idle instances (they become garbage); checked-out objects are untouched. */
  drain(): void {
    this.freelist.length = 0;
  }
}
