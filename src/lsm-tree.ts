import type { Entry } from './entry.ts';
import { MemTable } from './memtable.ts';
import { mergeEntries } from './merge.ts';
import { SSTable } from './sstable.ts';
import { TOMBSTONE, isTombstone } from './tombstone.ts';

export interface LSMTreeOptions {
  /** MemTable 记录数达到该阈值即 Flush 为 L0 SSTable。 */
  memtableFlushThreshold?: number;
  /** 每层允许的最大 SSTable 数量，超出即触发向下一层的 Compaction。 */
  maxTablesPerLevel?: number;
  /** 总层数；最后一层为 Bottom-most 层，Tombstone 在此处被物理擦除。 */
  maxLevels?: number;
}

export interface LevelStats {
  tables: number;
  entries: number;
  tombstones: number;
}

export interface LSMStats {
  memtable: { entries: number; tombstones: number };
  levels: LevelStats[];
  totalTombstones: number;
}

export interface PhysicalEntry {
  /** 'memtable' 或层号（0 为 L0，maxLevels-1 为 Bottom-most 层）。 */
  location: 'memtable' | number;
  entry: Entry;
}

/**
 * LSM 树删除语义管理内核。
 *
 * 读路径（屏蔽）：
 *  - Point Get：按 MemTable → L0 → L1 … 的顺序查找，首个命中即最新版本；
 *    命中 Tombstone 视为 NotFound。
 *  - Range Scan：K 路归并所有数据源，每 Key 取最新版本，过滤 Tombstone。
 *
 * 写路径：delete 与 put 一样只是向 MemTable 追加一条记录（Tombstone），
 * 因此删除与写入同价，且立即对后续读生效。
 *
 * Compaction（生命周期清理）：
 *  - 层内表数超阈值时，将该层表与下一层键范围重叠的表归并；
 *  - 归并目标为 Bottom-most 层时，该 Key 已不存在更老版本，
 *    Tombstone（及其遮蔽的历史版本）被安全物理擦除，释放空间。
 */
export class LSMTree {
  private memtable = new MemTable();
  private readonly levels: SSTable[][];
  private seq = 0;
  private readonly flushThreshold: number;
  private readonly maxTablesPerLevel: number;
  readonly maxLevels: number;

  constructor(options: LSMTreeOptions = {}) {
    this.flushThreshold = options.memtableFlushThreshold ?? 16;
    this.maxTablesPerLevel = options.maxTablesPerLevel ?? 2;
    this.maxLevels = options.maxLevels ?? 4;
    if (this.maxLevels < 2) {
      throw new Error('maxLevels must be >= 2');
    }
    this.levels = Array.from({ length: this.maxLevels }, () => []);
  }

  /** 写入（或覆盖）一个 Key。 */
  put(key: string, value: string): void {
    this.memtable.put(key, ++this.seq, value);
    if (this.memtable.size >= this.flushThreshold) this.flush();
  }

  /** 删除一个 Key：写入 Tombstone 标记，立即对读路径生效。 */
  delete(key: string): void {
    this.memtable.put(key, ++this.seq, TOMBSTONE);
    if (this.memtable.size >= this.flushThreshold) this.flush();
  }

  /** Point Get：返回最新可见值；Key 不存在或已被删除时返回 undefined。 */
  get(key: string): string | undefined {
    const entry = this.getEntry(key);
    if (entry === undefined || isTombstone(entry.value)) return undefined;
    return entry.value;
  }

  private getEntry(key: string): Entry | undefined {
    const fromMemtable = this.memtable.getEntry(key);
    if (fromMemtable !== undefined) return fromMemtable;
    // 上层数据一定比下层新；同层内（L0 表间可能重叠）取 seq 最大者。
    for (const level of this.levels) {
      let newest: Entry | undefined;
      for (const table of level) {
        const entry = table.getEntry(key);
        if (entry !== undefined && (newest === undefined || entry.seq > newest.seq)) {
          newest = entry;
        }
      }
      if (newest !== undefined) return newest;
    }
    return undefined;
  }

  /**
   * Range Scan：返回 [startKey, endKey]（闭区间，均可省略）内
   * 所有可见的 Key-Value，按 Key 升序；Tombstone 遮蔽的 Key 不出现。
   */
  scan(startKey?: string, endKey?: string): { key: string; value: string }[] {
    const sources: Iterable<Entry>[] = [this.memtable.sortedEntries()];
    for (const level of this.levels) {
      for (const table of level) sources.push(table);
    }
    const result: { key: string; value: string }[] = [];
    for (const entry of mergeEntries(sources)) {
      if (startKey !== undefined && entry.key < startKey) continue;
      if (endKey !== undefined && entry.key > endKey) continue;
      if (isTombstone(entry.value)) continue;
      result.push({ key: entry.key, value: entry.value });
    }
    return result;
  }

  /** 将 MemTable 落盘为 L0 SSTable，并触发溢出检查。 */
  flush(): void {
    if (this.memtable.size === 0) return;
    this.levels[0].unshift(new SSTable(this.memtable.sortedEntries()));
    this.memtable.clear();
    this.compact();
  }

  /** 溢出驱动的自动 Compaction：逐层检查，级联向下归并。 */
  compact(): void {
    for (let level = 0; level < this.maxLevels; level++) {
      if (this.levels[level].length > this.maxTablesPerLevel) {
        this.compactLevel(level);
        level = -1; // 归并可能使下一层溢出，从头复查
      }
    }
  }

  /**
   * 将指定层的全部 SSTable 归并到下一层（Bottom-most 层则原地归并）。
   * 仅当目标层为 Bottom-most 层时丢弃 Tombstone —— 此时更深层不可能
   * 存在更老版本，删除语义已被完全物化，物理擦除是安全的。
   */
  compactLevel(levelIndex: number): void {
    if (levelIndex < 0 || levelIndex >= this.maxLevels) {
      throw new RangeError(`invalid level: ${levelIndex}`);
    }
    const moving = this.levels[levelIndex];
    if (moving.length === 0) return;

    const targetIndex = Math.min(levelIndex + 1, this.maxLevels - 1);
    const inPlace = targetIndex === levelIndex;

    const inputs: SSTable[] = [...moving];
    let remaining: SSTable[] = [];
    if (!inPlace) {
      // Leveled Compaction：仅纳入下一层中键范围重叠的表。
      let rangeMin = moving[0].minKey;
      let rangeMax = moving[0].maxKey;
      for (const table of moving) {
        if (table.minKey < rangeMin) rangeMin = table.minKey;
        if (table.maxKey > rangeMax) rangeMax = table.maxKey;
      }
      for (const table of this.levels[targetIndex]) {
        if (table.maxKey >= rangeMin && table.minKey <= rangeMax) {
          inputs.push(table);
        } else {
          remaining.push(table);
        }
      }
    }

    const dropTombstones = targetIndex === this.maxLevels - 1;
    const merged: Entry[] = [];
    for (const entry of mergeEntries(inputs)) {
      if (dropTombstones && isTombstone(entry.value)) continue; // 物理擦除
      merged.push(entry);
    }

    this.levels[levelIndex] = [];
    const output = merged.length > 0 ? [new SSTable(merged)] : [];
    if (inPlace) {
      this.levels[levelIndex] = output;
    } else {
      this.levels[targetIndex] = [...remaining, ...output].sort((a, b) =>
        a.minKey < b.minKey ? -1 : 1,
      );
    }
  }

  /**
   * 全量 Compaction：把所有数据逐级压入 Bottom-most 层并归并为单张表。
   * 结束后，所有 Tombstone 及其遮蔽的历史版本均被物理擦除。
   */
  compactAll(): void {
    this.flush();
    for (let level = 0; level < this.maxLevels - 1; level++) {
      if (this.levels[level].length > 0) this.compactLevel(level);
    }
    const lastLevel = this.levels[this.maxLevels - 1];
    if (lastLevel.length > 1 || lastLevel.some((table) => table.tombstoneCount > 0)) {
      this.compactLevel(this.maxLevels - 1);
    }
  }

  /** 全库 Tombstone 总数（MemTable + 各层 SSTable），用于观测清理进度。 */
  countTombstones(): number {
    let count = this.memtable.tombstoneCount;
    for (const level of this.levels) {
      for (const table of level) count += table.tombstoneCount;
    }
    return count;
  }

  /** 遍历所有物理记录（含 Tombstone），用于审计与测试。 */
  *allEntries(): Generator<PhysicalEntry, void, unknown> {
    for (const entry of this.memtable.sortedEntries()) {
      yield { location: 'memtable', entry };
    }
    for (let level = 0; level < this.maxLevels; level++) {
      for (const table of this.levels[level]) {
        for (const entry of table) yield { location: level, entry };
      }
    }
  }

  stats(): LSMStats {
    const levels = this.levels.map((tables) => ({
      tables: tables.length,
      entries: tables.reduce((sum, table) => sum + table.size, 0),
      tombstones: tables.reduce((sum, table) => sum + table.tombstoneCount, 0),
    }));
    return {
      memtable: { entries: this.memtable.size, tombstones: this.memtable.tombstoneCount },
      levels,
      totalTombstones: this.countTombstones(),
    };
  }
}
