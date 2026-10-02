/**
 * Tombstone（墓碑）标记：LSM 树删除语义的核心。
 *
 * 删除一个 Key 时并不就地擦除数据，而是写入一条 value 为 TOMBSTONE 的
 * 特殊记录。读路径（Point Get / Range Scan）遇到 Tombstone 时将其视为
 * “该 Key 已被删除”，从而屏蔽所有更老版本；Compaction 在确认下层不存在
 * 更老版本后（即记录下沉到 Bottom-most 层）将其物理擦除，释放空间。
 */

export const TOMBSTONE: unique symbol = Symbol('TOMBSTONE');

export type Tombstone = typeof TOMBSTONE;

/** 存储单元：要么是真实值，要么是 Tombstone 标记。 */
export type Cell = string | Tombstone;

export function isTombstone(cell: Cell): cell is Tombstone {
  return cell === TOMBSTONE;
}
