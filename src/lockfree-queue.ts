/**
 * Bounded multi-producer / multi-consumer lock-free queue.
 *
 * This is Dmitry Vyukov's classic bounded MPMC queue: every cell carries a
 * monotonically increasing sequence number and the head/tail cursors are
 * advanced exclusively with CAS (`Atomics.compareExchange`). There is no
 * lock of any kind; contending threads simply retry their CAS, which makes
 * the queue lock-free (at least one thread always makes progress).
 *
 * The queue stores 32-bit integers and lives entirely inside a
 * `SharedArrayBuffer`, so it can be shared between `worker_threads`
 * without serialization. Object payloads are handled by storing an index
 * into an external pool (see the Paxos transport for an example).
 *
 * Note: positions are signed 32-bit and wrap at 2^31; the sequence
 * arithmetic is deliberately wrap-safe exactly like the original algorithm.
 */

const ENQUEUE_POS = 0;
const DEQUEUE_POS = 1;
const HEADER_INTS = 2;
const INTS_PER_CELL = 2; // [sequence, value]

function isPowerOfTwo(n: number): boolean {
  return n > 0 && (n & (n - 1)) === 0;
}

export class LockFreeQueue {
  readonly capacity: number;
  readonly buffer: SharedArrayBuffer;
  private readonly view: Int32Array;
  private readonly mask: number;

  /**
   * @param capacity must be a power of two >= 2
   * @param buffer   optional pre-allocated SharedArrayBuffer (attach mode:
   *                 the existing memory is used as-is, nothing is zeroed)
   */
  constructor(capacity: number, buffer?: SharedArrayBuffer) {
    if (!Number.isInteger(capacity) || capacity < 2 || !isPowerOfTwo(capacity)) {
      throw new RangeError(`capacity must be a power of two >= 2, got ${capacity}`);
    }
    this.capacity = capacity;
    this.mask = capacity - 1;
    const bytes = LockFreeQueue.requiredBytes(capacity);
    if (buffer !== undefined && buffer.byteLength < bytes) {
      throw new RangeError(
        `buffer too small: need ${bytes} bytes, got ${buffer.byteLength}`,
      );
    }
    this.buffer = buffer ?? new SharedArrayBuffer(bytes);
    this.view = new Int32Array(this.buffer);
    if (buffer === undefined) {
      Atomics.store(this.view, ENQUEUE_POS, 0);
      Atomics.store(this.view, DEQUEUE_POS, 0);
      for (let i = 0; i < capacity; i++) {
        Atomics.store(this.view, this.seqIndex(i), i);
      }
    }
  }

  static requiredBytes(capacity: number): number {
    return (HEADER_INTS + capacity * INTS_PER_CELL) * Int32Array.BYTES_PER_ELEMENT;
  }

  /** Attach a second handle to an existing shared queue. */
  static attach(buffer: SharedArrayBuffer, capacity: number): LockFreeQueue {
    return new LockFreeQueue(capacity, buffer);
  }

  private seqIndex(cell: number): number {
    return HEADER_INTS + cell * INTS_PER_CELL;
  }

  private valueIndex(cell: number): number {
    return HEADER_INTS + cell * INTS_PER_CELL + 1;
  }

  /**
   * Try to enqueue `value`. Returns false when the queue is full.
   * Lock-free: retries its CAS while other producers win the race.
   */
  enqueue(value: number): boolean {
    const view = this.view;
    let pos = Atomics.load(view, ENQUEUE_POS);
    for (;;) {
      const cell = pos & this.mask;
      const seqIdx = this.seqIndex(cell);
      const seq = Atomics.load(view, seqIdx);
      const dif = seq - pos;
      if (dif === 0) {
        if (Atomics.compareExchange(view, ENQUEUE_POS, pos, pos + 1) === pos) {
          Atomics.store(view, this.valueIndex(cell), value | 0);
          Atomics.store(view, seqIdx, pos + 1);
          return true;
        }
        pos = Atomics.load(view, ENQUEUE_POS); // lost the CAS race, reload
      } else if (dif < 0) {
        return false; // full
      } else {
        pos = Atomics.load(view, ENQUEUE_POS); // another producer moved on
      }
    }
  }

  /**
   * Try to dequeue. Returns the value, or null when the queue is empty.
   */
  dequeue(): number | null {
    const view = this.view;
    let pos = Atomics.load(view, DEQUEUE_POS);
    for (;;) {
      const cell = pos & this.mask;
      const seqIdx = this.seqIndex(cell);
      const seq = Atomics.load(view, seqIdx);
      const dif = seq - (pos + 1);
      if (dif === 0) {
        if (Atomics.compareExchange(view, DEQUEUE_POS, pos, pos + 1) === pos) {
          const value = Atomics.load(view, this.valueIndex(cell));
          Atomics.store(view, seqIdx, pos + this.capacity);
          return value;
        }
        pos = Atomics.load(view, DEQUEUE_POS);
      } else if (dif < 0) {
        return null; // empty
      } else {
        pos = Atomics.load(view, DEQUEUE_POS);
      }
    }
  }

  /** Approximate size (racy under concurrency, exact when quiescent). */
  size(): number {
    return Atomics.load(this.view, ENQUEUE_POS) - Atomics.load(this.view, DEQUEUE_POS);
  }

  isEmpty(): boolean {
    return this.size() === 0;
  }

  isFull(): boolean {
    return this.size() >= this.capacity;
  }

  /** Monotonic counters, useful for leak assertions in tests. */
  get enqueuePos(): number {
    return Atomics.load(this.view, ENQUEUE_POS);
  }

  get dequeuePos(): number {
    return Atomics.load(this.view, DEQUEUE_POS);
  }
}
