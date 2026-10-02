export interface VersionedValue {
  seq: number;
  value: string | null; // null = tombstone (delete)
}

export type Probe = { found: boolean; value: string | null };

/**
 * In-memory sorted write buffer owned by a single column family.
 * Every key keeps its versioned history so snapshots can read at an older seq.
 */
export class MemTable {
  private entries = new Map<string, VersionedValue[]>();

  put(key: string, seq: number, value: string | null): void {
    let versions = this.entries.get(key);
    if (!versions) {
      versions = [];
      this.entries.set(key, versions);
    }
    versions.push({ seq, value });
  }

  /** Newest version visible at `atSeq`; found=false means the key is absent. */
  probe(key: string, atSeq: number): Probe {
    const versions = this.entries.get(key);
    if (!versions) return { found: false, value: null };
    for (let i = versions.length - 1; i >= 0; i--) {
      const v = versions[i];
      if (v.seq <= atSeq) return { found: true, value: v.value };
    }
    return { found: false, value: null };
  }

  get(key: string, atSeq: number): string | undefined {
    const p = this.probe(key, atSeq);
    return p.found && p.value !== null ? p.value : undefined;
  }

  /** All live keys visible at `atSeq`, sorted. */
  keys(atSeq: number): string[] {
    const result: string[] = [];
    for (const key of this.entries.keys()) {
      if (this.get(key, atSeq) !== undefined) result.push(key);
    }
    return result.sort();
  }

  get size(): number {
    return this.entries.size;
  }

  /** Flatten to sorted [key, newestVersion] pairs for SSTable flush. */
  toSortedEntries(): Array<[string, VersionedValue]> {
    const out: Array<[string, VersionedValue]> = [];
    for (const [key, versions] of this.entries) {
      out.push([key, versions[versions.length - 1]]);
    }
    return out.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  }
}
