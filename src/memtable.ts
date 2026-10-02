export interface MemEntry {
  value: string | null; // null = tombstone (deleted)
  seq: number;
}

/** In-memory sorted write buffer owned by a single column family. */
export class MemTable {
  /** key -> versions, newest (highest seq) first. */
  private entries = new Map<string, MemEntry[]>();

  put(key: string, value: string | null, seq: number): void {
    const versions = this.entries.get(key) ?? [];
    versions.unshift({ value, seq });
    this.entries.set(key, versions);
  }

  get(key: string, maxSeq: number): MemEntry | undefined {
    return this.entries.get(key)?.find((e) => e.seq <= maxSeq);
  }

  get size(): number {
    return this.entries.size;
  }

  /** All versions, sorted by key then seq desc; used when flushing. */
  sortedEntries(): Array<[string, MemEntry]> {
    const out: Array<[string, MemEntry]> = [];
    for (const key of [...this.entries.keys()].sort()) {
      for (const e of this.entries.get(key)!) out.push([key, e]);
    }
    return out;
  }

  clear(): void {
    this.entries.clear();
  }
}
