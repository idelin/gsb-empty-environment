/**
 * chunk.ts —— 预分配固定大小的 Chunk 内存块与对象池。
 *
 * 设计要点（GC 规避）：
 *  - 池在构建期一次性预分配全部 Chunk（底层为定长 Float64Array）。
 *  - 稳态执行期间 acquire/release 只做数组 push/pop，零堆分配。
 *  - 池容量耗尽时拒绝扩容（抛错），从机制上保证"零分配"承诺不被悄悄破坏。
 */

/** 每个 Chunk 的元素槽位数（固定大小）。 */
export const CHUNK_SIZE = 1024;

/**
 * Chunk：一块固定大小的向量内存槽位。
 * `data` 在池初始化时分配，之后终身复用，永不重新分配。
 */
export class Chunk {
  /** 池内唯一编号，便于测试与调试时追踪复用情况。 */
  readonly id: number;
  /** 底层定长存储，容量恒为 CHUNK_SIZE。 */
  readonly data: Float64Array;
  /** 逻辑长度（本次写入的有效元素个数），acquire 时清零。 */
  length: number;

  constructor(id: number) {
    this.id = id;
    this.data = new Float64Array(CHUNK_SIZE);
    this.length = 0;
  }

  /** 归还前重置逻辑长度，物理内存保留复用。 */
  reset(): void {
    this.length = 0;
  }
}

export interface PoolStats {
  /** 池容量（Chunk 总数）。 */
  readonly capacity: number;
  /** 当前空闲槽位数。 */
  readonly freeCount: number;
  /** 当前被算子占用的槽位数。 */
  readonly inUseCount: number;
  /** 自池建立以来发生的真实堆分配次数（预分配阶段 = capacity，稳态应恒为 0 增长）。 */
  readonly heapAllocations: number;
  /** 累计 acquire 次数。 */
  readonly acquires: number;
  /** 累计 release 次数。 */
  readonly releases: number;
}

export class ChunkPool {
  private readonly free: Chunk[] = [];
  private readonly capacity: number;
  private heapAllocations = 0;
  private acquires = 0;
  private releases = 0;
  private inUse = 0;

  /**
   * 构建期一次性预分配 capacity 个 Chunk。
   * 这是整个查询生命周期中唯一发生堆分配的时刻。
   */
  constructor(capacity: number) {
    if (!Number.isInteger(capacity) || capacity <= 0) {
      throw new RangeError(`capacity 必须为正整数，收到: ${capacity}`);
    }
    this.capacity = capacity;
    for (let i = 0; i < capacity; i++) {
      this.free.push(new Chunk(i));
      this.heapAllocations++;
    }
  }

  /**
   * 借出一个 Chunk。稳态下仅做一次数组 pop，无任何堆分配。
   * 池耗尽时抛错而非扩容，确保零分配承诺可验证。
   */
  acquire(): Chunk {
    const chunk = this.free.pop();
    if (chunk === undefined) {
      throw new Error(
        `ChunkPool 耗尽（capacity=${this.capacity}）：拒绝在稳态执行期分配新内存`,
      );
    }
    chunk.reset();
    this.inUse++;
    this.acquires++;
    return chunk;
  }

  /** 归还 Chunk，槽位立即可供下游复用。 */
  release(chunk: Chunk): void {
    chunk.reset();
    this.free.push(chunk);
    this.inUse--;
    this.releases++;
  }

  stats(): PoolStats {
    return {
      capacity: this.capacity,
      freeCount: this.free.length,
      inUseCount: this.inUse,
      heapAllocations: this.heapAllocations,
      acquires: this.acquires,
      releases: this.releases,
    };
  }
}
