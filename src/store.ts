/**
 * 无锁 MVCC 核心存储（单写者 / 多读者，基于 SharedArrayBuffer + Atomics）。
 *
 * 无锁保证：
 * - 版本节点发布后不可变，版本链只追加（append-only）。
 * - 发布 = 一次 `Atomics.store` 头指针（release）；读者 `Atomics.load`
 *   头指针（acquire）后即可用普通读遍历整条链 —— 节点字段的写入在
 *   头指针发布之前完成，JS 内存模型保证其可见性。
 * - 只读事务 = 一次 `Atomics.load(COMMIT_SEQ)`，O(1)，不获取任何锁，
 *   不等待、不重试（wait-free）。
 * - 写事务先构造全部版本节点再单点推进 COMMIT_SEQ（提交点），
 *   读者凭快照序号过滤 txId，多键写入对读者原子可见。
 */

import { HEADER, NODE, poolBaseInts, requiredBytes } from './layout.ts';

export class MvccStore {
  readonly sab: SharedArrayBuffer;
  readonly keyCount: number;
  readonly poolCapacity: number;
  readonly i32: Int32Array;
  readonly f64: Float64Array;
  readonly poolBase: number;

  private constructor(sab: SharedArrayBuffer, keyCount: number, poolCapacity: number) {
    this.sab = sab;
    this.keyCount = keyCount;
    this.poolCapacity = poolCapacity;
    this.i32 = new Int32Array(sab);
    this.f64 = new Float64Array(sab);
    this.poolBase = poolBaseInts(keyCount);
  }

  /** 主线程：分配共享内存并创建存储。 */
  static create(keyCount: number, poolCapacity: number): MvccStore {
    return new MvccStore(
      new SharedArrayBuffer(requiredBytes(keyCount, poolCapacity)),
      keyCount,
      poolCapacity,
    );
  }

  /** worker 线程：附加到已有共享内存。 */
  static attach(sab: SharedArrayBuffer, keyCount: number, poolCapacity: number): MvccStore {
    return new MvccStore(sab, keyCount, poolCapacity);
  }

  private nodeBase(index: number): number {
    return this.poolBase + index * NODE.INTS;
  }

  private keyHeadSlot(key: number): number {
    return HEADER.SIZE + key;
  }

  /** 初始化全部键（仅在启动 worker 之前单线程调用）。 */
  initAll(value: number): void {
    for (let key = 0; key < this.keyCount; key++) {
      const index = Atomics.add(this.i32, HEADER.POOL_NEXT, 1);
      const base = this.nodeBase(index);
      this.f64[base >> 1] = value;
      this.i32[base + NODE.TX_ID] = 0;
      this.i32[base + NODE.PREV] = -1;
      this.i32[base + NODE.KEY] = key;
      Atomics.store(this.i32, this.keyHeadSlot(key), index);
    }
    Atomics.store(this.i32, HEADER.COMMIT_SEQ, 0);
  }

  // ---------------------------------------------------------------- 读路径

  /**
   * 开启只读事务：读取全局提交序号作为快照令牌。
   * 无锁、无等待、无重试，O(1)。
   */
  snapshot(): number {
    return Atomics.load(this.i32, HEADER.COMMIT_SEQ);
  }

  /**
   * 在快照 `snap` 下读取一个键。
   * 沿不可变版本链跳过 txId > snap 的节点，绝不阻塞。
   */
  read(snap: number, key: number): number {
    let index = Atomics.load(this.i32, this.keyHeadSlot(key));
    let base = this.nodeBase(index);
    while (this.i32[base + NODE.TX_ID] > snap) {
      index = this.i32[base + NODE.PREV];
      base = this.nodeBase(index);
    }
    return this.f64[base >> 1];
  }

  /** 全表扫描求和（只读事务内）。 */
  sum(snap: number): number {
    let total = 0;
    for (let key = 0; key < this.keyCount; key++) total += this.read(snap, key);
    return total;
  }

  // ---------------------------------------------------------------- 写路径

  /**
   * 提交一个写事务：为每个更新追加不可变版本节点，
   * 全部节点就绪后单点推进 COMMIT_SEQ —— 多键更新对读者原子可见。
   */
  commit(updates: Iterable<readonly [key: number, value: number]>): number {
    const txId = Atomics.load(this.i32, HEADER.COMMIT_SEQ) + 1;
    for (const [key, value] of updates) {
      this.appendVersion(key, value, txId);
    }
    Atomics.store(this.i32, HEADER.COMMIT_SEQ, txId);
    return txId;
  }

  /**
   * 低层接口：向键的版本链追加一个不可变节点（不推进提交序号）。
   * 用于 commit 内部，也用于测试“未提交节点对快照不可见”。
   */
  appendVersion(key: number, value: number, txId: number): void {
    const index = Atomics.add(this.i32, HEADER.POOL_NEXT, 1);
    if (index >= this.poolCapacity) {
      throw new RangeError(`version pool exhausted (capacity=${this.poolCapacity})`);
    }
    const base = this.nodeBase(index);
    const prev = Atomics.load(this.i32, this.keyHeadSlot(key));
    // 先写节点字段，最后发布头指针（release 语义）。
    this.f64[base >> 1] = value;
    this.i32[base + NODE.TX_ID] = txId;
    this.i32[base + NODE.KEY] = key;
    this.i32[base + NODE.PREV] = prev;
    Atomics.store(this.i32, this.keyHeadSlot(key), index);
  }

  /** 节点池剩余容量。 */
  remainingCapacity(): number {
    return this.poolCapacity - Atomics.load(this.i32, HEADER.POOL_NEXT);
  }

  // ---------------------------------------------------------------- 统计与协调

  markWriterDone(): void {
    Atomics.store(this.i32, HEADER.WRITER_DONE, 1);
  }

  isWriterDone(): boolean {
    return Atomics.load(this.i32, HEADER.WRITER_DONE) === 1;
  }

  recordScan(torn: boolean): void {
    Atomics.add(this.i32, HEADER.SCAN_COUNT, 1);
    if (torn) Atomics.add(this.i32, HEADER.TORN_COUNT, 1);
  }

  stats(): { commits: number; scans: number; torn: number; writerDone: boolean } {
    return {
      commits: Atomics.load(this.i32, HEADER.COMMIT_SEQ),
      scans: Atomics.load(this.i32, HEADER.SCAN_COUNT),
      torn: Atomics.load(this.i32, HEADER.TORN_COUNT),
      writerDone: this.isWriterDone(),
    };
  }
}
