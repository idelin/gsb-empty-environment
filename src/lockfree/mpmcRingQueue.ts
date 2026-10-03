/**
 * Dmitry Vyukov 有界 MPMC 无锁环形队列。
 *
 * 底层为 SharedArrayBuffer + Atomics，可跨 worker 线程共享；
 * 所有槽位带独立序列号，入队/出队仅依赖 Atomics.compareExchange，
 * 无互斥锁、无忙等之外的阻塞。元素为非负整数（用于传递索引/句柄）。
 */

const ENQUEUE_POS = 0;
const DEQUEUE_POS = 1;
const HEADER = 2;
const CELL_STRIDE = 2; // 每个槽位：[sequence, data]

export class MpmcRingQueue {
  static readonly EMPTY = -1;

  #cells: Int32Array;
  #capacity: number;
  #mask: number;

  private constructor(cells: Int32Array, capacity: number) {
    this.#cells = cells;
    this.#capacity = capacity;
    this.#mask = capacity - 1;
  }

  static create(capacity: number): MpmcRingQueue {
    if (!Number.isInteger(capacity) || capacity < 2 || (capacity & (capacity - 1)) !== 0) {
      throw new RangeError('capacity must be a power of two >= 2');
    }
    const buffer = new SharedArrayBuffer((HEADER + capacity * CELL_STRIDE) * Int32Array.BYTES_PER_ELEMENT);
    const cells = new Int32Array(buffer);
    for (let i = 0; i < capacity; i++) {
      cells[HEADER + i * CELL_STRIDE] = i; // 初始序列号 = 槽位下标
    }
    return new MpmcRingQueue(cells, capacity);
  }

  /** 依附到已存在的共享缓冲区（用于 worker 线程）。 */
  static attach(buffer: SharedArrayBuffer): MpmcRingQueue {
    const cells = new Int32Array(buffer);
    const capacity = (cells.length - HEADER) / CELL_STRIDE;
    if (!Number.isInteger(capacity) || capacity < 2 || (capacity & (capacity - 1)) !== 0) {
      throw new RangeError('buffer does not encode a valid MpmcRingQueue');
    }
    return new MpmcRingQueue(cells, capacity);
  }

  get buffer(): SharedArrayBuffer {
    return this.#cells.buffer as SharedArrayBuffer;
  }

  get capacity(): number {
    return this.#capacity;
  }

  /** 近似长度（并发下仅为观测瞬间值）。 */
  get size(): number {
    const enq = Atomics.load(this.#cells, ENQUEUE_POS);
    const deq = Atomics.load(this.#cells, DEQUEUE_POS);
    return Math.max(0, enq - deq);
  }

  get isEmpty(): boolean {
    return this.size === 0;
  }

  /** 入队；队列满时返回 false（调用方自旋重试或放弃）。 */
  enqueue(value: number): boolean {
    if (!Number.isInteger(value) || value < 0 || value > 0x7fffffff) {
      throw new RangeError('value must be a non-negative int32');
    }
    const cells = this.#cells;
    let pos = Atomics.load(cells, ENQUEUE_POS);
    for (;;) {
      const cell = HEADER + (pos & this.#mask) * CELL_STRIDE;
      const seq = Atomics.load(cells, cell);
      const dif = seq - pos;
      if (dif === 0) {
        if (Atomics.compareExchange(cells, ENQUEUE_POS, pos, pos + 1) === pos) {
          Atomics.store(cells, cell + 1, value);
          Atomics.store(cells, cell, pos + 1); // 发布：序列号 +1
          return true;
        }
        pos = Atomics.load(cells, ENQUEUE_POS);
      } else if (dif < 0) {
        return false; // 队列已满
      } else {
        pos = Atomics.load(cells, ENQUEUE_POS);
      }
    }
  }

  /** 出队；队列空时返回 MpmcRingQueue.EMPTY。 */
  dequeue(): number {
    const cells = this.#cells;
    let pos = Atomics.load(cells, DEQUEUE_POS);
    for (;;) {
      const cell = HEADER + (pos & this.#mask) * CELL_STRIDE;
      const seq = Atomics.load(cells, cell);
      const dif = seq - (pos + 1);
      if (dif === 0) {
        if (Atomics.compareExchange(cells, DEQUEUE_POS, pos, pos + 1) === pos) {
          const value = Atomics.load(cells, cell + 1);
          Atomics.store(cells, cell, pos + this.#capacity); // 槽位复位到下一周期
          return value;
        }
        pos = Atomics.load(cells, DEQUEUE_POS);
      } else if (dif < 0) {
        return MpmcRingQueue.EMPTY; // 队列已空
      } else {
        pos = Atomics.load(cells, DEQUEUE_POS);
      }
    }
  }
}
