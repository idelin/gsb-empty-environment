/**
 * Bounded lock-free MPMC ring buffer (Dmitry Vyukov's algorithm) backed by a
 * SharedArrayBuffer, so it can be shared directly between worker threads.
 *
 * Every slot carries a monotonically increasing sequence number; producers
 * and consumers coordinate exclusively through Atomics.compareExchange on
 * the head/tail cursors — a genuine lock-free, CAS-accelerated queue with
 * zero mutexes.
 */

const HEADER_INTS = 2; // [enqueuePos, dequeuePos]
const INTS_PER_CELL = 2; // [sequence, value]

export class MPMCRing {
  readonly capacity: number;
  private readonly mask: number;
  private readonly header: Int32Array;
  private readonly cells: Int32Array;

  private constructor(buffer: SharedArrayBuffer, capacity: number) {
    if (!Number.isInteger(capacity) || capacity < 2 || (capacity & (capacity - 1)) !== 0) {
      throw new RangeError('capacity must be a power of two >= 2');
    }
    this.capacity = capacity;
    this.mask = capacity - 1;
    this.header = new Int32Array(buffer, 0, HEADER_INTS);
    this.cells = new Int32Array(buffer, HEADER_INTS * 4, capacity * INTS_PER_CELL);
  }

  static create(capacity: number): MPMCRing {
    const buffer = new SharedArrayBuffer((HEADER_INTS + capacity * INTS_PER_CELL) * 4);
    const ring = new MPMCRing(buffer, capacity);
    for (let i = 0; i < capacity; i++) {
      Atomics.store(ring.cells, i * INTS_PER_CELL, i);
    }
    return ring;
  }

  /** Re-attach to an existing ring from another thread. */
  static from(buffer: SharedArrayBuffer, capacity: number): MPMCRing {
    return new MPMCRing(buffer, capacity);
  }

  get buffer(): SharedArrayBuffer {
    return this.header.buffer as SharedArrayBuffer;
  }

  /** Returns false when the ring is full (never blocks). */
  enqueue(value: number): boolean {
    let pos = Atomics.load(this.header, 0);
    for (;;) {
      const cell = (pos & this.mask) * INTS_PER_CELL;
      const seq = Atomics.load(this.cells, cell);
      const dif = seq - pos;
      if (dif === 0) {
        if (Atomics.compareExchange(this.header, 0, pos, pos + 1) === pos) {
          Atomics.store(this.cells, cell + 1, value | 0);
          Atomics.store(this.cells, cell, pos + 1);
          return true;
        }
        pos = Atomics.load(this.header, 0);
      } else if (dif < 0) {
        return false; // full
      } else {
        pos = Atomics.load(this.header, 0);
      }
    }
  }

  /** Returns undefined when the ring is empty (never blocks). */
  dequeue(): number | undefined {
    let pos = Atomics.load(this.header, 1);
    for (;;) {
      const cell = (pos & this.mask) * INTS_PER_CELL;
      const seq = Atomics.load(this.cells, cell);
      const dif = seq - (pos + 1);
      if (dif === 0) {
        if (Atomics.compareExchange(this.header, 1, pos, pos + 1) === pos) {
          const value = Atomics.load(this.cells, cell + 1);
          Atomics.store(this.cells, cell, pos + this.capacity);
          return value;
        }
        pos = Atomics.load(this.header, 1);
      } else if (dif < 0) {
        return undefined; // empty
      } else {
        pos = Atomics.load(this.header, 1);
      }
    }
  }

  /** Approximate occupancy (racy by design, for observability only). */
  get size(): number {
    return Atomics.load(this.header, 0) - Atomics.load(this.header, 1);
  }

  get isEmpty(): boolean {
    return this.size === 0;
  }
}
