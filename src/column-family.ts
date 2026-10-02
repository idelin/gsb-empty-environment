import { MemTable } from "./memtable.ts";
import { SsTable } from "./sstable.ts";

/**
 * A logically isolated namespace inside the shared LSM instance.
 * Owns a private MemTable and a private list of SSTable metadata;
 * only the WAL is shared with other families.
 */
export class ColumnFamily {
  readonly name: string;
  private memtable = new MemTable();
  private sstables: SsTable[] = []; // newest first

  constructor(name: string) {
    this.name = name;
  }

  applyPut(key: string, seq: number, value: string | null): void {
    this.memtable.put(key, seq, value);
  }

  /** Read newest-first: active MemTable, then SSTables from newest to oldest. */
  get(key: string, atSeq: number): string | undefined {
    const fromMem = this.memtable.probe(key, atSeq);
    if (fromMem.found) return fromMem.value === null ? undefined : fromMem.value;
    for (const sst of this.sstables) {
      const p = sst.probe(key, atSeq);
      if (p.found) return p.value === null ? undefined : p.value;
    }
    return undefined;
  }

  /** Live keys visible at `atSeq`, across MemTable + SSTables, sorted. */
  keys(atSeq: number): string[] {
    const merged = new Map<string, string | undefined>();
    for (let i = this.sstables.length - 1; i >= 0; i--) {
      for (const k of this.sstables[i].keys(atSeq)) {
        if (!merged.has(k)) merged.set(k, this.get(k, atSeq));
      }
    }
    for (const k of this.memtable.keys(atSeq)) merged.set(k, this.get(k, atSeq));
    return [...merged.entries()]
      .filter(([, v]) => v !== undefined)
      .map(([k]) => k)
      .sort();
  }

  get sstableCount(): number {
    return this.sstables.length;
  }

  get memtableSize(): number {
    return this.memtable.size;
  }

  /** Flush the active MemTable into a new immutable SSTable. */
  flush(filePath: string): SsTable {
    const sst = SsTable.write(filePath, this.memtable.toSortedEntries());
    this.sstables.unshift(sst);
    this.memtable = new MemTable();
    return sst;
  }

  attachSsTable(sst: SsTable): void {
    this.sstables.unshift(sst);
  }
}
