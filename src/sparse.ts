/**
 * 海量稀疏数据的内存紧凑优化器。
 *
 * SparsePage 在两种物理编码之间自适应转换：
 *  - sparse: Map<offset, value>，仅保存非默认值槽位，适合极低密度；
 *  - dense : Float64Array，定长连续数组，适合高密度，避免哈希表摊销。
 *
 * 转换完全由内存核算驱动，并带滞后带（hysteresis）防止阈值附近抖动：
 *  - sparse -> dense: sparseBytes > DENSE_BYTES * (1 + H)
 *  - dense -> sparse: sparseBytes < DENSE_BYTES * (1 - H)
 *
 * 序列化使用行程长度编码（RLE），对稀疏/常量分布极度紧凑。
 * AllocationTracker 用于测试期断言严格无泄漏。
 */

export interface RLEBlock {
  start: number;
  length: number;
  value: number;
}

export class AllocationTracker {
  private static liveCount = 0;
  static track(): void {
    AllocationTracker.liveCount++;
  }
  static release(): void {
    AllocationTracker.liveCount--;
  }
  static get live(): number {
    return AllocationTracker.liveCount;
  }
}

export class SparsePage {
  static readonly CAPACITY = 4096;
  static readonly DEFAULT_VALUE = 0;

  private static readonly DENSE_BYTES = SparsePage.CAPACITY * 8;
  private static readonly SPARSE_ENTRY_BYTES = 32;
  private static readonly HYSTERESIS = 0.1;

  readonly pageId: number;
  pageLSN = 0;

  private dense: Float64Array | null = null;
  private sparse: Map<number, number> | null = new Map();
  private nonDefault = 0;
  private disposed = false;

  constructor(pageId: number) {
    this.pageId = pageId;
    AllocationTracker.track();
  }

  get encoding(): 'sparse' | 'dense' {
    return this.dense !== null ? 'dense' : 'sparse';
  }

  get size(): number {
    return this.nonDefault;
  }

  get isDisposed(): boolean {
    return this.disposed;
  }

  memoryBytes(): number {
    if (this.disposed) return 0;
    return this.dense !== null
      ? SparsePage.DENSE_BYTES
      : this.nonDefault * SparsePage.SPARSE_ENTRY_BYTES;
  }

  private assertUsable(offset: number): void {
    if (this.disposed) throw new Error(`page ${this.pageId} has been disposed`);
    if (!Number.isInteger(offset) || offset < 0 || offset >= SparsePage.CAPACITY) {
      throw new RangeError(`offset ${offset} out of range [0, ${SparsePage.CAPACITY})`);
    }
  }

  read(offset: number): number {
    this.assertUsable(offset);
    if (this.dense !== null) return this.dense[offset];
    return this.sparse!.get(offset) ?? SparsePage.DEFAULT_VALUE;
  }

  write(offset: number, value: number): void {
    this.assertUsable(offset);
    const current = this.read(offset);
    if (current === value) return;
    this.setRaw(offset, value);
    this.rebalance();
  }

  private setRaw(offset: number, value: number): void {
    const current = this.dense !== null
      ? this.dense[offset]
      : this.sparse!.get(offset) ?? SparsePage.DEFAULT_VALUE;
    if (this.dense !== null) {
      this.dense[offset] = value;
    } else if (value === SparsePage.DEFAULT_VALUE) {
      this.sparse!.delete(offset);
    } else {
      this.sparse!.set(offset, value);
    }
    if (current === SparsePage.DEFAULT_VALUE && value !== SparsePage.DEFAULT_VALUE) {
      this.nonDefault++;
    } else if (current !== SparsePage.DEFAULT_VALUE && value === SparsePage.DEFAULT_VALUE) {
      this.nonDefault--;
    }
  }

  private rebalance(): void {
    const sparseBytes = this.nonDefault * SparsePage.SPARSE_ENTRY_BYTES;
    if (this.dense === null && sparseBytes > SparsePage.DENSE_BYTES * (1 + SparsePage.HYSTERESIS)) {
      const arr = new Float64Array(SparsePage.CAPACITY);
      for (const [offset, value] of this.sparse!) arr[offset] = value;
      this.dense = arr;
      this.sparse = null;
    } else if (this.dense !== null && sparseBytes < SparsePage.DENSE_BYTES * (1 - SparsePage.HYSTERESIS)) {
      const map = new Map<number, number>();
      for (let i = 0; i < SparsePage.CAPACITY; i++) {
        const value = this.dense[i];
        if (value !== SparsePage.DEFAULT_VALUE) map.set(i, value);
      }
      this.sparse = map;
      this.dense = null;
    }
  }

  *entries(): IterableIterator<[number, number]> {
    this.assertUsable(0);
    if (this.dense !== null) {
      for (let i = 0; i < SparsePage.CAPACITY; i++) {
        if (this.dense[i] !== SparsePage.DEFAULT_VALUE) yield [i, this.dense[i]];
      }
    } else {
      yield* this.sparse!;
    }
  }

  toRLE(): RLEBlock[] {
    this.assertUsable(0);
    const blocks: RLEBlock[] = [];
    let start = 0;
    let current = this.read(0);
    for (let i = 1; i < SparsePage.CAPACITY; i++) {
      const value = this.read(i);
      if (value !== current) {
        blocks.push({ start, length: i - start, value: current });
        start = i;
        current = value;
      }
    }
    blocks.push({ start, length: SparsePage.CAPACITY - start, value: current });
    return blocks;
  }

  static fromRLE(pageId: number, blocks: RLEBlock[], pageLSN = 0): SparsePage {
    const page = new SparsePage(pageId);
    let expected = 0;
    for (const block of blocks) {
      if (block.start !== expected || !Number.isInteger(block.length) || block.length <= 0) {
        page.dispose();
        throw new Error(`invalid RLE stream at block start=${block.start}`);
      }
      if (block.value !== SparsePage.DEFAULT_VALUE) {
        for (let i = 0; i < block.length; i++) page.setRaw(block.start + i, block.value);
      }
      expected += block.length;
    }
    if (expected !== SparsePage.CAPACITY) {
      page.dispose();
      throw new Error(`invalid RLE stream: covers ${expected} slots, expected ${SparsePage.CAPACITY}`);
    }
    page.pageLSN = pageLSN;
    page.rebalance();
    return page;
  }

  dispose(): void {
    if (this.disposed) return;
    this.dense = null;
    this.sparse = null;
    this.disposed = true;
    AllocationTracker.release();
  }
}

/** 异步互斥闩锁：保证协作式调度下 read-modify-write 临界区不被交错打断。 */
export class AsyncLatch {
  private tail: Promise<void> = Promise.resolve();

  async acquire(): Promise<() => void> {
    let release!: () => void;
    const next = new Promise<void>((resolve) => {
      release = resolve;
    });
    const prev = this.tail;
    this.tail = this.tail.then(() => next);
    await prev;
    return release;
  }
}

const yieldControl = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

/** 带闩锁的页包装器：并发 read-modify-write 安全。 */
export class LatchedSparsePage {
  private readonly latch = new AsyncLatch();
  readonly page: SparsePage;

  constructor(page: SparsePage) {
    this.page = page;
  }

  async update(offset: number, fn: (current: number) => number): Promise<void> {
    const release = await this.latch.acquire();
    try {
      const current = this.page.read(offset);
      await yieldControl(); // 模拟异步工作；无闩锁时此处会发生交错丢更新
      this.page.write(offset, fn(current));
    } finally {
      release();
    }
  }
}
