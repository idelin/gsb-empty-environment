export type MemTableState = 'active' | 'immutable' | 'flushing' | 'flushed';

export interface MemTableEntry {
  key: string;
  value: Buffer | null; // null = tombstone
}

const ENTRY_OVERHEAD_BYTES = 32;

export class MemTable {
  readonly id: number;
  state: MemTableState = 'active';
  private readonly entries = new Map<string, Buffer | null>();
  private bytes = 0;

  constructor(id: number) {
    this.id = id;
  }

  get sizeBytes(): number {
    return this.bytes;
  }

  get entryCount(): number {
    return this.entries.size;
  }

  put(key: string, value: Buffer): void {
    this.apply(key, value);
  }

  delete(key: string): void {
    this.apply(key, null);
  }

  private apply(key: string, value: Buffer | null): void {
    if (this.entries.has(key)) {
      const prev = this.entries.get(key)!;
      this.bytes -= Buffer.byteLength(key) + (prev === null ? 0 : prev.length) + ENTRY_OVERHEAD_BYTES;
    }
    this.entries.set(key, value);
    this.bytes += Buffer.byteLength(key) + (value === null ? 0 : value.length) + ENTRY_OVERHEAD_BYTES;
  }

  get(key: string): Buffer | null | undefined {
    if (!this.entries.has(key)) return undefined;
    return this.entries.get(key)!;
  }

  sortedEntries(): MemTableEntry[] {
    return [...this.entries.entries()]
      .map(([key, value]) => ({ key, value }))
      .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  }
}
