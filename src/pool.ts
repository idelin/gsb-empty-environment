/**
 * ChunkPool: pre-allocated fixed-size chunk memory pool.
 *
 * All chunk backing buffers (Float64Array) are allocated up-front at
 * construction time. During query execution, `acquire()` / `release()`
 * only move chunk slots between the free stack and the caller — no heap
 * allocation happens on the hot path, so the GC is never pressured.
 */

export interface Chunk {
  /** Stable slot identity; reused across acquire/release cycles. */
  readonly id: number;
  /** Backing store, pre-allocated at pool construction. */
  readonly data: Float64Array;
  /** Number of valid rows currently stored in `data`. */
  length: number;
  /** Debug guard against double-release / use-after-release. */
  released: boolean;
}

export interface PoolStats {
  readonly capacity: number;
  readonly chunkSize: number;
  readonly acquireCount: number;
  readonly releaseCount: number;
  /** Chunks created after construction — must stay 0 in steady state. */
  readonly allocations: number;
  readonly inFlight: number;
}

export class ChunkPool {
  readonly capacity: number;
  readonly chunkSize: number;

  private readonly chunks: Chunk[];
  /** Free-slot stack of chunk ids; pre-sized, pointer-bumped (no push/pop). */
  private readonly stack: Int32Array;
  private top: number;

  /** Overflow slots for chunks created by (unexpected) pool growth. */
  private readonly overflow: Chunk[];

  private acquireCount = 0;
  private releaseCount = 0;
  private allocations = 0;

  constructor(capacity: number, chunkSize: number) {
    if (capacity <= 0 || chunkSize <= 0) {
      throw new Error('capacity and chunkSize must be positive');
    }
    this.capacity = capacity;
    this.chunkSize = chunkSize;
    this.chunks = new Array<Chunk>(capacity);
    this.stack = new Int32Array(capacity);
    this.overflow = [];
    for (let i = 0; i < capacity; i++) {
      this.chunks[i] = {
        id: i,
        data: new Float64Array(chunkSize),
        length: 0,
        released: true,
      };
      this.stack[i] = capacity - 1 - i;
    }
    this.top = capacity;
  }

  /**
   * Borrow a chunk. Hot path: pop a slot id off the free stack — zero
   * allocation. Only if the pool is exhausted does it grow (counted in
   * `allocations`, which the acceptance criteria require to stay 0).
   */
  acquire(): Chunk {
    this.acquireCount++;
    if (this.top === 0) {
      const overflow = this.overflow.pop();
      if (overflow !== undefined) {
        overflow.released = false;
        overflow.length = 0;
        return overflow;
      }
      this.allocations++;
      const chunk: Chunk = {
        id: this.capacity + this.allocations - 1,
        data: new Float64Array(this.chunkSize),
        length: 0,
        released: false,
      };
      return chunk;
    }
    const chunk = this.chunks[this.stack[--this.top]];
    chunk.released = false;
    chunk.length = 0;
    return chunk;
  }

  /** Return a chunk to the pool so downstream operators can reuse the slot. */
  release(chunk: Chunk): void {
    if (chunk.released) {
      throw new Error(`double release of chunk ${chunk.id}`);
    }
    this.releaseCount++;
    chunk.released = true;
    chunk.length = 0;
    if (chunk.id < this.capacity) {
      this.stack[this.top++] = chunk.id;
    } else {
      this.overflow.push(chunk);
    }
  }

  stats(): PoolStats {
    return {
      capacity: this.capacity,
      chunkSize: this.chunkSize,
      acquireCount: this.acquireCount,
      releaseCount: this.releaseCount,
      allocations: this.allocations,
      inFlight: this.acquireCount - this.releaseCount,
    };
  }
}
