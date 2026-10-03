import { estimateEntryBytes } from "./memory.ts";
import type { Dot, Entry } from "./types.ts";

export class CrdtStore<V = unknown> {
  private entries = new Map<string, Entry<V>>();
  private totalBytes = 0;

  set(key: string, value: V): void {
    const existing = this.entries.get(key);
    if (existing) this.totalBytes -= existing.sizeBytes;
    const entry: Entry<V> = {
      key,
      value,
      tombstone: false,
      deletedAt: null,
      sizeBytes: estimateEntryBytes(key, value),
    };
    this.entries.set(key, entry);
    this.totalBytes += entry.sizeBytes;
  }

  delete(key: string, dot: Dot): boolean {
    const existing = this.entries.get(key);
    if (!existing || existing.tombstone) return false;
    this.totalBytes -= existing.sizeBytes;
    const tombstone: Entry<V> = {
      key,
      value: null,
      tombstone: true,
      deletedAt: dot,
      sizeBytes: estimateEntryBytes(key, null),
    };
    this.entries.set(key, tombstone);
    this.totalBytes += tombstone.sizeBytes;
    return true;
  }

  purge(key: string): boolean {
    const existing = this.entries.get(key);
    if (!existing) return false;
    this.entries.delete(key);
    this.totalBytes -= existing.sizeBytes;
    return true;
  }

  get(key: string): Entry<V> | undefined {
    return this.entries.get(key);
  }

  tombstones(): Entry<V>[] {
    return [...this.entries.values()].filter((e) => e.tombstone);
  }

  get size(): number {
    return this.entries.size;
  }

  get bytes(): number {
    return this.totalBytes;
  }
}
