import type { ClockSnapshot } from "./vectorClock.ts";

export interface Tombstone {
  key: string;
  deletedBy: string;
  /** Causal (vector) time at which the delete was issued. */
  clock: ClockSnapshot;
  /** Deletion metadata retained so the delete can still propagate. */
  payload: string;
  bytes: number;
}

export interface PruneResult {
  removed: number;
  bytesFreed: number;
}

export interface StoreStats {
  liveEntries: number;
  tombstones: number;
  liveBytes: number;
  tombstoneBytes: number;
  totalBytes: number;
}

const EMPTY_PRUNE: PruneResult = { removed: 0, bytesFreed: 0 };

/**
 * LWW-style key/value store whose deletes leave tombstones behind.
 * Tracks an in-memory byte budget so GC impact is measurable.
 */
export class TombstoneStore {
  private readonly live = new Map<string, { value: string; bytes: number }>();
  private readonly tombstones = new Map<string, Tombstone>();
  private liveBytes = 0;
  private tombstoneBytes = 0;
  private readonly tombstonePayloadSize: number;

  constructor(tombstonePayloadSize = 256) {
    this.tombstonePayloadSize = tombstonePayloadSize;
  }

  set(key: string, value: string): void {
    const existing = this.live.get(key);
    if (existing) this.liveBytes -= existing.bytes;
    const bytes = Buffer.byteLength(key) + Buffer.byteLength(value);
    this.live.set(key, { value, bytes });
    this.liveBytes += bytes;
  }

  /** Delete a live key, retaining a tombstone stamped with the causal time. */
  remove(key: string, deletedBy: string, clock: ClockSnapshot): boolean {
    const existing = this.live.get(key);
    if (!existing) return false;
    this.live.delete(key);
    this.liveBytes -= existing.bytes;

    const payload = `${deletedBy}:${key}:`.padEnd(this.tombstonePayloadSize, "x");
    const bytes =
      Buffer.byteLength(key) + Buffer.byteLength(payload) + 8 /* clock overhead */;
    this.tombstones.set(key, { key, deletedBy, clock, payload, bytes });
    this.tombstoneBytes += bytes;
    return true;
  }

  isDeleted(key: string): boolean {
    return this.tombstones.has(key);
  }

  get(key: string): string | undefined {
    return this.live.get(key)?.value;
  }

  tombstoneClocks(): Tombstone[] {
    return [...this.tombstones.values()];
  }

  /**
   * Physically drop every tombstone accepted by `stable`.
   * This is the point of no return: memory is released for good.
   */
  prune(stable: (tombstone: Tombstone) => boolean): PruneResult {
    if (this.tombstones.size === 0) return EMPTY_PRUNE;
    let removed = 0;
    let bytesFreed = 0;
    for (const [key, tombstone] of this.tombstones) {
      if (stable(tombstone)) {
        this.tombstones.delete(key);
        this.tombstoneBytes -= tombstone.bytes;
        removed += 1;
        bytesFreed += tombstone.bytes;
      }
    }
    return removed === 0 ? EMPTY_PRUNE : { removed, bytesFreed };
  }

  stats(): StoreStats {
    return {
      liveEntries: this.live.size,
      tombstones: this.tombstones.size,
      liveBytes: this.liveBytes,
      tombstoneBytes: this.tombstoneBytes,
      totalBytes: this.liveBytes + this.tombstoneBytes,
    };
  }
}
