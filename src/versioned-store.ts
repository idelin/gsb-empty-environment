interface Slot {
  version: number;
  present: boolean;
}

/**
 * Last-write-wins map keyed by an opaque string, with tombstones.
 *
 * A `remove` leaves a tombstone carrying its version, so a delayed
 * older `upsert` cannot resurrect the entry. Ties are resolved in
 * favour of removal (absent) for determinism; equal-version duplicates
 * are dropped.
 */
export class VersionedStore<K extends string> {
  private slots = new Map<K, Slot>();
  private droppedCount = 0;

  /** Returns true when the event was newer than the stored state. */
  apply(key: K, version: number, present: boolean): boolean {
    const current = this.slots.get(key);
    if (current !== undefined) {
      if (version < current.version) {
        this.droppedCount++;
        return false;
      }
      if (version === current.version) {
        // Duplicate, or a conflicting tie: removal wins, upsert drops.
        if (current.present === present || present) {
          this.droppedCount++;
          return false;
        }
      }
    }
    this.slots.set(key, { version, present });
    return true;
  }

  isPresent(key: K): boolean {
    const slot = this.slots.get(key);
    return slot !== undefined && slot.present;
  }

  versionOf(key: K): number | undefined {
    return this.slots.get(key)?.version;
  }

  /** Events rejected because a newer (or equal) state was already stored. */
  dropped(): number {
    return this.droppedCount;
  }

  liveEntries(): Array<{ key: K; version: number }> {
    const out: Array<{ key: K; version: number }> = [];
    for (const [key, slot] of this.slots) {
      if (slot.present) out.push({ key, version: slot.version });
    }
    return out;
  }
}
