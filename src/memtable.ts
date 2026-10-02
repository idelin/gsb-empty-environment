import type { Entry } from './entry.ts';
import { isTombstone, type Cell } from './tombstone.ts';

/**
 * MemTable：写入缓冲。同一 Key 只保留最新一条记录（后写覆盖先写，
 * 包括用 Tombstone 覆盖旧值）。
 */
export class MemTable {
  private readonly map = new Map<string, Entry>();

  put(key: string, seq: number, value: Cell): void {
    this.map.set(key, { key, seq, value });
  }

  getEntry(key: string): Entry | undefined {
    return this.map.get(key);
  }

  get size(): number {
    return this.map.size;
  }

  get tombstoneCount(): number {
    let count = 0;
    for (const entry of this.map.values()) {
      if (isTombstone(entry.value)) count++;
    }
    return count;
  }

  /** 按 Key 升序导出全部记录（Flush 时据此构建有序 SSTable）。 */
  sortedEntries(): Entry[] {
    return [...this.map.values()].sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  }

  clear(): void {
    this.map.clear();
  }
}
