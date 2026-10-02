export type MemTableState = 'active' | 'frozen' | 'flushing' | 'flushed';

export const ENTRY_OVERHEAD_BYTES = 16;

/**
 * In-memory sorted table. Pure data structure: the engine owns the WAL
 * and drives the state machine active -> frozen -> flushing -> flushed.
 */
export class MemTable {
  state: MemTableState = 'active';
  sizeBytes = 0;
  private readonly map = new Map<string, Buffer | null>();
  readonly id: number;
  readonly thresholdBytes: number;

  constructor(id: number, thresholdBytes: number) {
    this.id = id;
    this.thresholdBytes = thresholdBytes;
  }

  get isFull(): boolean {
    return this.sizeBytes >= this.thresholdBytes;
  }

  get entryCount(): number {
    return this.map.size;
  }

  /** Apply a WAL-confirmed write. value === null is a tombstone. */
  apply(key: string, value: Buffer | null): void {
    if (this.map.has(key)) {
      const previous = this.map.get(key)!;
      this.sizeBytes -= Buffer.byteLength(key) + (previous === null ? 0 : previous.length) + ENTRY_OVERHEAD_BYTES;
    }
    this.map.set(key, value);
    this.sizeBytes += Buffer.byteLength(key) + (value === null ? 0 : value.length) + ENTRY_OVERHEAD_BYTES;
  }

  /** Returns Buffer for a hit, null for a tombstone, undefined for a miss. */
  get(key: string): Buffer | null | undefined {
    return this.map.get(key);
  }

  sortedEntries(): [string, Buffer | null][] {
    return [...this.map.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  }
}
