interface Entry {
  seq: number;
  value: string;
  prev: Entry | null;
}

export interface Snapshot {
  seq: number;
  pageCapacity: number;
  pages: Array<Array<[string, string]>>;
  keyCount: number;
}

/**
 * In-memory ordered state tree with MVCC-style versioned entries.
 * set() never mutates existing entry objects, so a snapshot taken at seq S
 * stays a consistent, read-only view while writers keep appending versions.
 */
export class StateTree {
  private data = new Map<string, Entry>();
  private seq = 0;
  private snapshotsInFlight = 0;
  private lastSnapshotSeq = 0;

  constructor(public readonly pageCapacity: number = 256) {}

  get currentSeq(): number {
    return this.seq;
  }

  get size(): number {
    return this.data.size;
  }

  set(key: string, value: string): number {
    const s = ++this.seq;
    const prev = this.data.get(key) || null;
    this.data.set(key, { seq: s, value, prev });
    return s;
  }

  get(key: string): string | undefined {
    const entry = this.data.get(key);
    return entry ? entry.value : undefined;
  }

  /** Capture a consistent read-only snapshot, sliced into sorted pages. */
  snapshot(): Snapshot {
    const snapSeq = this.seq;
    this.snapshotsInFlight++;
    this.lastSnapshotSeq = snapSeq;
    const visible = new Map<string, string>();
    for (const pair of this.data) {
      const key = pair[0];
      let entry: Entry | null = pair[1];
      while (entry !== null && entry.seq > snapSeq) {
        entry = entry.prev;
      }
      if (entry !== null) {
        visible.set(key, entry.value);
      }
    }
    const keys = Array.from(visible.keys()).sort();
    const pages: Array<Array<[string, string]>> = [];
    for (let i = 0; i < keys.length; i += this.pageCapacity) {
      const page: Array<[string, string]> = [];
      const end = Math.min(i + this.pageCapacity, keys.length);
      for (let j = i; j < end; j++) {
        const key = keys[j];
        page.push([key, visible.get(key) as string]);
      }
      pages.push(page);
    }
    return { seq: snapSeq, pageCapacity: this.pageCapacity, pages, keyCount: keys.length };
  }

  /** Called when a checkpoint finished using its snapshot. */
  releaseSnapshot(): void {
    this.snapshotsInFlight--;
    if (this.snapshotsInFlight === 0) {
      this.collapseHistory();
    }
  }

  /** Drop version-chain nodes no snapshot can ever observe again. */
  private collapseHistory(): void {
    const horizon = this.lastSnapshotSeq;
    for (const pair of this.data) {
      let entry: Entry | null = pair[1];
      while (entry !== null && entry.seq > horizon) {
        entry = entry.prev;
      }
      if (entry !== null && entry.prev !== null) {
        entry.prev = null;
      }
    }
  }
}
