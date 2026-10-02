/**
 * 共享内存布局（单个 SharedArrayBuffer，Int32 槽位寻址）。
 *
 * ┌─────────────────────────────────────────────┐
 * │ Header (16 个 Int32 槽位)                    │
 * │   [0] COMMIT_SEQ   全局提交序号（快照令牌）   │
 * │   [1] POOL_NEXT    版本节点池分配指针         │
 * │   [2] WRITER_DONE  写线程结束标志             │
 * │   [3] SCAN_COUNT   读线程完成的扫描总数       │
 * │   [4] TORN_COUNT   撕裂读计数（验收指标）     │
 * ├─────────────────────────────────────────────┤
 * │ Key Directory (keyCount 个 Int32 槽位)       │
 * │   每个键一个原子头指针 → 版本链最新节点       │
 * ├─────────────────────────────────────────────┤
 * │ Version Node Pool (poolCapacity 个定长节点)  │
 * │   节点 = 24 字节，追加式分配，发布后不可变    │
 * └─────────────────────────────────────────────┘
 */

export const HEADER = {
  COMMIT_SEQ: 0,
  POOL_NEXT: 1,
  WRITER_DONE: 2,
  SCAN_COUNT: 3,
  TORN_COUNT: 4,
  SIZE: 16,
} as const;

/** 版本节点：24 字节 = 6 个 Int32 槽位。 */
export const NODE = {
  /** Float64 值，占用槽位 0..1 */
  VALUE: 0,
  /** 提交该版本的事务 ID */
  TX_ID: 2,
  /** 同一键的上一个版本节点索引（-1 表示链尾） */
  PREV: 3,
  /** 所属键（调试用） */
  KEY: 4,
  /** 节点跨度（Int32 槽位数） */
  INTS: 6,
} as const;

const alignEven = (n: number): number => (n + 1) & ~1;

/** 节点池起始槽位（偶数对齐，保证 Float64 视图 8 字节对齐）。 */
export function poolBaseInts(keyCount: number): number {
  return alignEven(HEADER.SIZE + keyCount);
}

/** 计算所需 SharedArrayBuffer 字节数。 */
export function requiredBytes(keyCount: number, poolCapacity: number): number {
  return (poolBaseInts(keyCount) + poolCapacity * NODE.INTS) * 4;
}
