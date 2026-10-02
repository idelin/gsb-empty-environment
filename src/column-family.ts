import { MemTable, type MemEntry } from './memtable.ts';
import type { SSTable } from './sstable.ts';

/** Handle naming a logical column family inside one LSM instance. */
export interface ColumnFamilyHandle {
  readonly name: string;
}

/** Per-CF state: private MemTable + private SSTable metadata. */
export class ColumnFamilyState {
  readonly handle: ColumnFamilyHandle;
  memtable = new MemTable();
  /** Newest first. */
  sstables: SSTable[] = [];
  private nextSstId = 1;

  constructor(name: string) {
    this.handle = { name };
  }

  allocSstId(): number {
    return this.nextSstId++;
  }

  observeSstId(id: number): void {
    this.nextSstId = Math.max(this.nextSstId, id + 1);
  }

  /** Read path: MemTable first, then SSTables newest to oldest. */
  get(key: string, maxSeq: number): MemEntry | undefined {
    const fromMem = this.memtable.get(key, maxSeq);
    if (fromMem) return fromMem;
    for (const sst of this.sstables) {
      const found = sst.get(key, maxSeq);
      if (found) return found;
    }
    return undefined;
  }
}
