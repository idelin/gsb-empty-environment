import { ObjectPool } from "./pool.ts";
import { ZeroAllocCache } from "./cache.ts";
import { resetLogEntry } from "./types.ts";
import type { Ballot, LogEntry } from "./types.ts";

/**
 * LogStore — sparse replicated log backed by a pooled, cache-accelerated
 * LogEntry set.
 *
 * Memory discipline:
 *  - Every LogEntry comes from the ObjectPool and is returned exactly once
 *    (truncate/dispose). outstanding count is the leak oracle.
 *  - Overwriting a dirty (uncommitted, minority) entry REUSES the same
 *    instance in place — the hot repair path allocates nothing.
 *  - The ZeroAllocCache is a pure read index over store-owned entries; it
 *    never owns them (no onEvict release), so eviction cannot corrupt state.
 */
export class LogStore {
  /** Highest slot known to be committed on this node (-1 = none). */
  commitIndex = -1;

  private readonly entries = new Map<number, LogEntry>();
  private readonly pool: ObjectPool<LogEntry>;
  private readonly cache: ZeroAllocCache<LogEntry>;
  private maxSlotSeen = -1;

  constructor(pool: ObjectPool<LogEntry>, cache: ZeroAllocCache<LogEntry>) {
    this.pool = pool;
    this.cache = cache;
  }

  get size(): number {
    return this.entries.size;
  }

  maxAcceptedSlot(): number {
    return this.maxSlotSeen;
  }

  /** Cache-first read. Hot path allocates nothing. */
  get(slot: number): LogEntry | undefined {
    const cached = this.cache.get(slot);
    if (cached !== undefined) return cached;
    const e = this.entries.get(slot);
    if (e !== undefined) this.cache.put(slot, e);
    return e;
  }

  /**
   * Accept (phase-2 write) an entry. If a dirty entry already occupies the
   * slot, its pooled instance is reused in place — zero allocation repair.
   * Returns whether a previously accepted value was overwritten.
   */
  accept(slot: number, ballot: Ballot, value: string): boolean {
    const existing = this.entries.get(slot);
    if (existing !== undefined) {
      if (existing.committed) {
        if (existing.value !== value) {
          // Paxos safety violation: a chosen value must never change.
          throw new Error(
            `committed entry at slot ${slot} contradicted: "${existing.value}" vs "${value}"`
          );
        }
        // Idempotent re-accept of an already-chosen value: keep committed.
        existing.ballotRound = ballot.round;
        existing.ballotNode = ballot.nodeId;
        return false;
      }
      const overwritten =
        existing.value !== value ||
        existing.ballotRound !== ballot.round ||
        existing.ballotNode !== ballot.nodeId;
      existing.ballotRound = ballot.round;
      existing.ballotNode = ballot.nodeId;
      existing.value = value;
      if (slot > this.maxSlotSeen) this.maxSlotSeen = slot;
      return overwritten;
    }
    const e = this.pool.acquire();
    e.slot = slot;
    e.ballotRound = ballot.round;
    e.ballotNode = ballot.nodeId;
    e.value = value;
    e.committed = false;
    this.entries.set(slot, e);
    this.cache.put(slot, e);
    if (slot > this.maxSlotSeen) this.maxSlotSeen = slot;
    return false;
  }

  /**
   * Mark every present entry in (commitIndex, index] committed, invoking
   * onCommit in slot order. Stops at the first hole — commits are contiguous.
   * Returns the new commitIndex.
   */
  commitUpTo(index: number, onCommit: (entry: LogEntry) => void): number {
    let slot = this.commitIndex + 1;
    while (slot <= index) {
      const e = this.entries.get(slot);
      if (e === undefined) break; // hole: cannot commit past it
      if (!e.committed) {
        e.committed = true;
        onCommit(e);
      }
      slot++;
    }
    this.commitIndex = slot - 1;
    return this.commitIndex;
  }

  /** Snapshot of accepted-but-uncommitted entries (election path; may allocate). */
  uncommittedAccepted(): LogEntry[] {
    const out: LogEntry[] = [];
    for (const e of this.entries.values()) {
      if (!e.committed) out.push(e);
    }
    out.sort((a, b) => a.slot - b.slot);
    return out;
  }

  /** Remove and release every entry at slot >= fromSlot. */
  truncateFrom(fromSlot: number): void {
    for (const [slot, e] of this.entries) {
      if (slot >= fromSlot) {
        this.entries.delete(slot);
        this.cache.delete(slot);
        this.pool.release(e);
      }
    }
    if (fromSlot <= this.maxSlotSeen) {
      this.maxSlotSeen = -1;
      for (const slot of this.entries.keys()) {
        if (slot > this.maxSlotSeen) this.maxSlotSeen = slot;
      }
    }
  }

  /** Release all entries to the pool. After dispose, pool.outstanding must be 0. */
  dispose(): void {
    for (const e of this.entries.values()) this.pool.release(e);
    this.entries.clear();
    this.cache.clear();
    this.maxSlotSeen = -1;
    this.commitIndex = -1;
  }
}

/** Convenience factory: a store with its own dedicated pool + cache. */
export function makeLogStore(name: string, cacheCapacity = 1024): {
  store: LogStore;
  pool: ObjectPool<LogEntry>;
  cache: ZeroAllocCache<LogEntry>;
} {
  const pool = new ObjectPool<LogEntry>({
    name: `${name}-entries`,
    capacity: 4096,
    prealloc: 64,
    factory: (): LogEntry => ({
      slot: -1,
      ballotRound: -1,
      ballotNode: -1,
      value: "",
      committed: false,
    }),
    reset: resetLogEntry,
  });
  const cache = new ZeroAllocCache<LogEntry>(cacheCapacity);
  return { store: new LogStore(pool, cache), pool, cache };
}
