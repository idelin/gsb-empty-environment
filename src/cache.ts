/**
 * Process-wide LRU cache of decoded blocks, shared across tenants.
 * Keyed by file/shard/block. Capacity is counted in decoded bytes (ids * 4).
 * Per-tenant memory quotas are enforced separately by the query Session;
 * this cache only bounds total retained decoded bytes.
 */
export class BlockCache {
  private entries = new Map<string, { ids: Uint32Array; bytes: number }>();
  private usedBytes = 0;
  readonly capacityBytes: number;
  hits = 0;
  misses = 0;

  constructor(capacityBytes: number) {
    if (!Number.isInteger(capacityBytes) || capacityBytes < 0) {
      throw new Error('cache capacity must be a non-negative integer');
    }
    this.capacityBytes = capacityBytes;
  }

  get size(): number {
    return this.entries.size;
  }

  get bytes(): number {
    return this.usedBytes;
  }

  get(key: string): Uint32Array | undefined {
    const e = this.entries.get(key);
    if (!e) {
      this.misses++;
      return undefined;
    }
    this.hits++;
    // refresh LRU position
    this.entries.delete(key);
    this.entries.set(key, e);
    return e.ids;
  }

  put(key: string, ids: Uint32Array): void {
    const bytes = ids.length * 4;
    const existing = this.entries.get(key);
    if (existing) {
      this.entries.delete(key);
      this.usedBytes -= existing.bytes;
    }
    if (bytes > this.capacityBytes) return; // too large to retain; still usable by the caller
    while (this.usedBytes + bytes > this.capacityBytes && this.entries.size > 0) {
      const oldestKey = this.entries.keys().next().value as string;
      const oldest = this.entries.get(oldestKey);
      if (oldest) this.usedBytes -= oldest.bytes;
      this.entries.delete(oldestKey);
    }
    this.entries.set(key, { ids, bytes });
    this.usedBytes += bytes;
  }

  clear(): void {
    this.entries.clear();
    this.usedBytes = 0;
  }
}
