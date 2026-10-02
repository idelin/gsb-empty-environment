import type { Entry } from './entry.ts';
import { isTombstone } from './tombstone.ts';

let nextTableId = 0;

/**
 * SSTable：不可变的有序记录集合（按 Key 升序）。
 * 由 MemTable Flush 或 Compaction 合并产生，落盘后不再修改。
 */
export class SSTable implements Iterable<Entry> {
  readonly id: number;
  readonly entries: readonly Entry[];

  constructor(entries: readonly Entry[]) {
    if (entries.length === 0) {
      throw new Error('SSTable must contain at least one entry');
    }
    this.id = nextTableId++;
    this.entries = entries;
  }

  get size(): number {
    return this.entries.length;
  }

  get minKey(): string {
    return this.entries[0].key;
  }

  get maxKey(): string {
    return this.entries[this.entries.length - 1].key;
  }

  get tombstoneCount(): number {
    let count = 0;
    for (const entry of this.entries) {
      if (isTombstone(entry.value)) count++;
    }
    return count;
  }

  /** 二分查找指定 Key 的记录（找不到返回 undefined）。 */
  getEntry(key: string): Entry | undefined {
    let lo = 0;
    let hi = this.entries.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const midKey = this.entries[mid].key;
      if (midKey === key) return this.entries[mid];
      if (midKey < key) lo = mid + 1;
      else hi = mid - 1;
    }
    return undefined;
  }

  *[Symbol.iterator](): Generator<Entry, void, unknown> {
    yield* this.entries;
  }
}
