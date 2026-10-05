/**
 * Grouped aggregation over a mutable event stream.
 *
 * Events are inserted and retracted by id. Per group the aggregator maintains
 * count, sum, min and the top-2 (smallest) entries. Retraction is fully
 * supported: state is an exact, bounded index of live events (a sorted
 * multiset per group), never a monotonic summary, so removing the current
 * min correctly reveals the next-smallest value.
 */

export interface RankedEntry {
  readonly eventId: string;
  readonly value: number;
}

export interface GroupStats {
  readonly group: string;
  readonly count: number;
  readonly sum: number;
  readonly min: number | null;
  /** Up to two smallest entries, ties broken deterministically by eventId. */
  readonly top2: ReadonlyArray<RankedEntry>;
}

export class DuplicateEventError extends Error {
  constructor(eventId: string) {
    super(`event "${eventId}" already exists`);
    this.name = "DuplicateEventError";
  }
}

export class UnknownEventError extends Error {
  constructor(eventId: string) {
    super(`event "${eventId}" does not exist (unknown or already retracted)`);
    this.name = "UnknownEventError";
  }
}

interface EventRecord {
  group: string;
  value: number;
}

interface Slot {
  value: number;
  eventId: string;
}

function compareSlots(a: Slot, b: Slot): number {
  if (a.value !== b.value) return a.value - b.value;
  return a.eventId < b.eventId ? -1 : a.eventId > b.eventId ? 1 : 0;
}

/**
 * Per-group exact index: a sorted array of live (value, eventId) slots.
 * Size is bounded by the number of live events in the group and shrinks on
 * retraction; the group itself is dropped from the aggregator when empty.
 */
class GroupState {
  count = 0;
  sum = 0;
  private slots: Slot[] = [];

  get size(): number {
    return this.slots.length;
  }

  add(slot: Slot): void {
    const at = this.lowerBound(slot);
    this.slots.splice(at, 0, slot);
    this.count += 1;
    this.sum += slot.value;
  }

  remove(slot: Slot): void {
    const at = this.lowerBound(slot);
    const found = this.slots[at];
    if (found === undefined || compareSlots(found, slot) !== 0) {
      throw new Error(`index desync: slot for event "${slot.eventId}" not found`);
    }
    this.slots.splice(at, 1);
    this.count -= 1;
    this.sum -= slot.value;
  }

  min(): number | null {
    const first = this.slots[0];
    return first === undefined ? null : first.value;
  }

  top2(): RankedEntry[] {
    return this.slots.slice(0, 2).map((s) => ({ eventId: s.eventId, value: s.value }));
  }

  private lowerBound(target: Slot): number {
    let lo = 0;
    let hi = this.slots.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (compareSlots(this.slots[mid]!, target) < 0) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }
}

export class GroupedAggregator {
  private readonly events = new Map<string, EventRecord>();
  private readonly groups = new Map<string, GroupState>();

  /** Number of live events across all groups. */
  get size(): number {
    return this.events.size;
  }

  /** Number of non-empty groups. */
  get groupCount(): number {
    return this.groups.size;
  }

  has(eventId: string): boolean {
    return this.events.has(eventId);
  }

  insert(eventId: string, group: string, value: number): void {
    if (this.events.has(eventId)) throw new DuplicateEventError(eventId);
    this.assertFinite(value);
    this.events.set(eventId, { group, value });
    this.stateFor(group).add({ value, eventId });
  }

  /**
   * Retract an event. Retracting an id that is not live (never inserted or
   * already retracted) is rejected, so duplicate retractions cannot silently
   * corrupt the aggregates.
   */
  retract(eventId: string): void {
    const record = this.events.get(eventId);
    if (record === undefined) throw new UnknownEventError(eventId);
    this.applyRetraction(eventId, record);
  }

  /**
   * Atomically re-assign an event to a (possibly different) group with a
   * (possibly new) value. The change is validated before any mutation, so a
   * failing update leaves every aggregate untouched; a successful one is
   * applied as a single retract+insert pair with no observable intermediate
   * state.
   */
  update(eventId: string, group: string, value: number): void {
    const record = this.events.get(eventId);
    if (record === undefined) throw new UnknownEventError(eventId);
    this.assertFinite(value);
    if (record.group === group && record.value === value) return;
    this.applyRetraction(eventId, record);
    this.events.set(eventId, { group, value });
    this.stateFor(group).add({ value, eventId });
  }

  /** Atomically move an event to another group, keeping its value. */
  moveToGroup(eventId: string, group: string): void {
    const record = this.events.get(eventId);
    if (record === undefined) throw new UnknownEventError(eventId);
    this.update(eventId, group, record.value);
  }

  /** Stats for a group, or undefined if the group has no live events. */
  stats(group: string): GroupStats | undefined {
    const state = this.groups.get(group);
    if (state === undefined) return undefined;
    return {
      group,
      count: state.count,
      sum: state.sum,
      min: state.min(),
      top2: state.top2(),
    };
  }

  /** Diagnostic: total indexed slots; always equals the live event count. */
  indexSize(): number {
    let total = 0;
    for (const state of this.groups.values()) total += state.size;
    return total;
  }

  private applyRetraction(eventId: string, record: EventRecord): void {
    const state = this.groups.get(record.group);
    if (state === undefined) {
      throw new Error(`index desync: group "${record.group}" missing`);
    }
    state.remove({ value: record.value, eventId });
    this.events.delete(eventId);
    if (state.size === 0) this.groups.delete(record.group);
  }

  private stateFor(group: string): GroupState {
    let state = this.groups.get(group);
    if (state === undefined) {
      state = new GroupState();
      this.groups.set(group, state);
    }
    return state;
  }

  private assertFinite(value: number): void {
    if (!Number.isFinite(value)) {
      throw new RangeError(`value must be finite, got ${value}`);
    }
  }
}
