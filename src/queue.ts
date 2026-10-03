const ENQ_POS = 0;
const DEQ_POS = 16;
const HEADER_INTS = 32;

const EMPTY = Symbol('empty');

/**
 * Bounded lock-free multi-producer multi-consumer queue (Vyukov algorithm)
 * over a SharedArrayBuffer. All coordination uses CAS on head/tail positions
 * plus per-cell sequence numbers; there is no global mutex.
 */
export class AtomicMPMCQueue {
  readonly capacity: number;
  private readonly mask: number;
  private readonly i32: Int32Array;
  private readonly seqBase: number;
  private readonly dataBase: number;

  static byteLength(capacity: number): number {
    AtomicMPMCQueue.checkCapacity(capacity);
    return (HEADER_INTS + capacity * 2) * 4;
  }

  private static checkCapacity(capacity: number): void {
    if (
      !Number.isInteger(capacity) ||
      capacity < 2 ||
      (capacity & (capacity - 1)) !== 0
    ) {
      throw new RangeError('capacity must be a power of two and at least 2');
    }
  }

  constructor(capacity: number, sab?: SharedArrayBuffer) {
    AtomicMPMCQueue.checkCapacity(capacity);
    this.capacity = capacity;
    this.mask = capacity - 1;
    const bytes = AtomicMPMCQueue.byteLength(capacity);
    const buffer = sab ?? new SharedArrayBuffer(bytes);
    if (buffer.byteLength < bytes) {
      throw new RangeError('SharedArrayBuffer too small for capacity');
    }
    this.i32 = new Int32Array(buffer);
    this.seqBase = HEADER_INTS;
    this.dataBase = HEADER_INTS + capacity;
    if (!sab) {
      for (let i = 0; i < capacity; i++) {
        Atomics.store(this.i32, this.seqBase + i, i);
      }
    }
  }

  static fromSharedArrayBuffer(
    sab: SharedArrayBuffer,
    capacity: number,
  ): AtomicMPMCQueue {
    return new AtomicMPMCQueue(capacity, sab);
  }

  get buffer(): SharedArrayBuffer {
    return this.i32.buffer as SharedArrayBuffer;
  }

  enqueue(value: number): boolean {
    let pos = Atomics.load(this.i32, ENQ_POS);
    for (;;) {
      const cell = pos & this.mask;
      const seq = Atomics.load(this.i32, this.seqBase + cell);
      const dif = seq - pos;
      if (dif === 0) {
        if (Atomics.compareExchange(this.i32, ENQ_POS, pos, pos + 1) === pos) {
          Atomics.store(this.i32, this.dataBase + cell, value | 0);
          Atomics.store(this.i32, this.seqBase + cell, pos + 1);
          return true;
        }
      } else if (dif < 0) {
        return false;
      }
      pos = Atomics.load(this.i32, ENQ_POS);
    }
  }

  dequeue(): number | undefined {
    let pos = Atomics.load(this.i32, DEQ_POS);
    for (;;) {
      const cell = pos & this.mask;
      const seq = Atomics.load(this.i32, this.seqBase + cell);
      const dif = seq - (pos + 1);
      if (dif === 0) {
        if (Atomics.compareExchange(this.i32, DEQ_POS, pos, pos + 1) === pos) {
          const value = Atomics.load(this.i32, this.dataBase + cell);
          Atomics.store(this.i32, this.seqBase + cell, pos + this.mask + 1);
          return value;
        }
      } else if (dif < 0) {
        return undefined;
      }
      pos = Atomics.load(this.i32, DEQ_POS);
    }
  }

  get size(): number {
    const enq = Atomics.load(this.i32, ENQ_POS);
    const deq = Atomics.load(this.i32, DEQ_POS);
    return Math.max(0, Math.min(this.capacity, enq - deq));
  }

  get isEmpty(): boolean {
    return this.size === 0;
  }

  get isFull(): boolean {
    return this.size === this.capacity;
  }
}

/**
 * Generic lock-free queue for arbitrary values: an AtomicMPMCQueue of slot
 * indices into an object pool, plus a second atomic ring as the free list.
 * Dequeued slots are cleared immediately so no references leak.
 */
export class LockFreeQueue<T> {
  readonly capacity: number;
  private readonly pool: (T | undefined)[];
  private readonly dataRing: AtomicMPMCQueue;
  private readonly freeRing: AtomicMPMCQueue;

  constructor(capacity: number) {
    this.capacity = capacity;
    this.pool = new Array<T | undefined>(capacity);
    this.dataRing = new AtomicMPMCQueue(capacity);
    this.freeRing = new AtomicMPMCQueue(capacity);
    for (let i = 0; i < capacity; i++) {
      if (!this.freeRing.enqueue(i)) throw new Error('free ring init failed');
    }
  }

  enqueue(value: T): boolean {
    const slot = this.freeRing.dequeue();
    if (slot === undefined) return false;
    this.pool[slot] = value;
    if (!this.dataRing.enqueue(slot)) {
      this.pool[slot] = undefined;
      this.freeRing.enqueue(slot);
      return false;
    }
    return true;
  }

  dequeue(): T | undefined {
    const slot = this.dataRing.dequeue();
    if (slot === undefined) return undefined;
    const value = this.pool[slot];
    this.pool[slot] = undefined;
    this.freeRing.enqueue(slot);
    return value;
  }

  get size(): number {
    return this.dataRing.size;
  }

  get isEmpty(): boolean {
    return this.dataRing.isEmpty;
  }

  get isFull(): boolean {
    return this.dataRing.isFull;
  }

  countLiveSlots(): number {
    let live = 0;
    for (const v of this.pool) if (v !== undefined) live++;
    return live;
  }
}

/**
 * Coarse-grained global-mutex baseline (Atomics spin lock + wait/notify),
 * kept for throughput comparison against the lock-free queue.
 */
export class MutexQueue {
  private readonly i32: Int32Array;
  readonly capacity: number;

  static byteLength(capacity: number): number {
    return (8 + capacity) * 4;
  }

  constructor(capacity: number, sab?: SharedArrayBuffer) {
    this.capacity = capacity;
    this.i32 = new Int32Array(sab ?? new SharedArrayBuffer(MutexQueue.byteLength(capacity)));
  }

  static fromSharedArrayBuffer(sab: SharedArrayBuffer, capacity: number): MutexQueue {
    return new MutexQueue(capacity, sab);
  }

  get buffer(): SharedArrayBuffer {
    return this.i32.buffer as SharedArrayBuffer;
  }

  private get head(): number {
    return this.i32[1];
  }
  private set head(v: number) {
    this.i32[1] = v;
  }
  private get tail(): number {
    return this.i32[2];
  }
  private set tail(v: number) {
    this.i32[2] = v;
  }
  private get count(): number {
    return this.i32[3];
  }
  private set count(v: number) {
    this.i32[3] = v;
  }

  private lock(): void {
    let spins = 0;
    for (;;) {
      if (Atomics.compareExchange(this.i32, 0, 0, 1) === 0) return;
      if (++spins > 64) {
        try {
          Atomics.wait(this.i32, 0, 1);
        } catch {
          // main thread cannot block; keep spinning
        }
        spins = 0;
      }
    }
  }

  private unlock(): void {
    Atomics.store(this.i32, 0, 0);
    Atomics.notify(this.i32, 0, 1);
  }

  enqueue(value: number): boolean {
    this.lock();
    try {
      if (this.count === this.capacity) return false;
      this.i32[8 + this.tail] = value | 0;
      this.tail = (this.tail + 1) % this.capacity;
      this.count++;
      return true;
    } finally {
      this.unlock();
    }
  }

  dequeue(): number | undefined {
    this.lock();
    try {
      if (this.count === 0) return undefined;
      const value = this.i32[8 + this.head];
      this.head = (this.head + 1) % this.capacity;
      this.count--;
      return value;
    } finally {
      this.unlock();
    }
  }
}

export { EMPTY };
