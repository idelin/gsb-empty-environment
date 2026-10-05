/**
 * Grouped aggregation with exact retract semantics.
 *
 * - insert / retract by event id
 * - per-group aggregates: count, sum, min, top-2 (dense ranks, ties included)
 * - events can change group via a single atomic update
 * - bounded index: maxGroups + maxEventsPerGroup
 * - empty groups are deleted eagerly
 *
 * Retraction is exact: every group's values are kept in a real multiset
 * (value -> id set, plus a sorted list of distinct values). We deliberately
 * do NOT use append-only/monotonic summaries, because min and top-2 cannot
 * be maintained by a grow-only sketch once removals are allowed.
 */

export type ErrorCode =
  | 'DUPLICATE_EVENT_ID'
  | 'UNKNOWN_EVENT_ID'
  | 'GROUP_LIMIT_EXCEEDED'
  | 'GROUP_CAPACITY_EXCEEDED'
  | 'INVALID_VALUE';

export class AggregatorError extends Error {
  readonly code: ErrorCode;
  constructor(code: ErrorCode, message: string) {
    super(message);
    this.name = 'AggregatorError';
    this.code = code;
  }
}

export interface EventRecord {
  id: string;
  group: string;
  value: number;
}

export interface RankEntry {
  value: number;
  ids: string[];
}

export interface GroupStats {
  group: string;
  count: number;
  sum: number;
  min: number;
  /** Up to two dense ranks, descending by value. Ties share a rank. */
  top: RankEntry[];
}

export interface AggregatorOptions {
  /** Maximum number of distinct groups. Default: Infinity. */
  maxGroups?: number;
  /** Maximum number of live events per group. Default: Infinity. */
  maxEventsPerGroup?: number;
}

interface EventLocation {
  group: string;
  value: number;
}

/** Exact, removable multiset of (value -> event ids) with sorted distinct values. */
class GroupState {
  count = 0;
  sum = 0;
  /** Distinct values, sorted descending. */
  private distinctDesc: number[] = [];
  private idsByValue = new Map<number, Set<string>>();

  add(id: string, value: number): void {
    let ids = this.idsByValue.get(value);
    if (ids === undefined) {
      ids = new Set<string>();
      this.idsByValue.set(value, ids);
      this.distinctDesc.splice(this.insertionIndex(value), 0, value);
    }
    ids.add(id);
    this.count += 1;
    this.sum += value;
  }

  remove(id: string, value: number): void {
    const ids = this.idsByValue.get(value);
    if (ids === undefined || !ids.delete(id)) {
      throw new AggregatorError(
        'UNKNOWN_EVENT_ID',
        `event "${id}" with value ${value} is not present in this group`,
      );
    }
    if (ids.size === 0) {
      this.idsByValue.delete(value);
      this.distinctDesc.splice(this.distinctDesc.indexOf(value), 1);
    }
    this.count -= 1;
    this.sum -= value;
  }

  get min(): number {
    return this.distinctDesc[this.distinctDesc.length - 1];
  }

  topTwo(): RankEntry[] {
    const out: RankEntry[] = [];
    for (let i = 0; i < this.distinctDesc.length && i < 2; i += 1) {
      const value = this.distinctDesc[i];
      out.push({ value, ids: [...this.idsByValue.get(value)!].sort() });
    }
    return out;
  }

  /** Binary search: first index whose value <= target (descending order). */
  private insertionIndex(value: number): number {
    let lo = 0;
    let hi = this.distinctDesc.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.distinctDesc[mid] > value) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }
}

export class GroupedAggregator {
  private readonly maxGroups: number;
  private readonly maxEventsPerGroup: number;
  private readonly events = new Map<string, EventLocation>();
  private readonly groups = new Map<string, GroupState>();

  constructor(options: AggregatorOptions = {}) {
    this.maxGroups = options.maxGroups ?? Infinity;
    this.maxEventsPerGroup = options.maxEventsPerGroup ?? Infinity;
  }

  get groupCount(): number {
    return this.groups.size;
  }

  get eventCount(): number {
    return this.events.size;
  }

  hasEvent(id: string): boolean {
    return this.events.has(id);
  }

  hasGroup(group: string): boolean {
    return this.groups.has(group);
  }

  insert(event: EventRecord): void {
    const { id, group, value } = event;
    assertFinite(value);
    if (this.events.has(id)) {
      throw new AggregatorError('DUPLICATE_EVENT_ID', `event "${id}" already exists`);
    }
    this.assertCanAdmit(group);
    this.stateFor(group).add(id, value);
    this.events.set(id, { group, value });
  }

  /**
   * Retract an event by id. Retracting the same id twice (or an id that was
   * never inserted) is rejected with UNKNOWN_EVENT_ID.
   */
  retract(id: string): void {
    const loc = this.events.get(id);
    if (loc === undefined) {
      throw new AggregatorError('UNKNOWN_EVENT_ID', `cannot retract unknown event "${id}"`);
    }
    const state = this.groups.get(loc.group)!;
    state.remove(id, loc.value);
    this.events.delete(id);
    if (state.count === 0) {
      this.groups.delete(loc.group); // empty groups are deleted eagerly
    }
  }

  /**
   * Atomically change an event's group and/or value. All validation happens
   * before any mutation, so a failed update leaves the index untouched:
   * the event keeps its old group and value.
   */
  update(id: string, changes: { group?: string; value?: number }): void {
    const loc = this.events.get(id);
    if (loc === undefined) {
      throw new AggregatorError('UNKNOWN_EVENT_ID', `cannot update unknown event "${id}"`);
    }
    const nextGroup = changes.group ?? loc.group;
    const nextValue = changes.value ?? loc.value;
    assertFinite(nextValue);

    // Validate first: this is what makes the group change atomic.
    if (nextGroup !== loc.group) {
      this.assertCanAdmit(nextGroup);
    }

    if (nextGroup === loc.group && nextValue === loc.value) return;

    const source = this.groups.get(loc.group)!;
    source.remove(id, loc.value);
    if (source.count === 0) {
      this.groups.delete(loc.group);
    }
    this.stateFor(nextGroup).add(id, nextValue);
    this.events.set(id, { group: nextGroup, value: nextValue });
  }

  /** Convenience wrapper for the atomic group change. */
  moveToGroup(id: string, group: string): void {
    this.update(id, { group });
  }

  /** Stats for a live group, or undefined for an unknown/empty group. */
  getGroupStats(group: string): GroupStats | undefined {
    const state = this.groups.get(group);
    if (state === undefined) return undefined;
    return {
      group,
      count: state.count,
      sum: state.sum,
      min: state.min,
      top: state.topTwo(),
    };
  }

  listGroups(): string[] {
    return [...this.groups.keys()].sort();
  }

  /** Bounded-index guard: new group allowed? group has capacity? */
  private assertCanAdmit(group: string): void {
    const existing = this.groups.get(group);
    if (existing === undefined) {
      if (this.groups.size >= this.maxGroups) {
        throw new AggregatorError(
          'GROUP_LIMIT_EXCEEDED',
          `cannot admit group "${group}": index is bounded to ${this.maxGroups} groups`,
        );
      }
    } else if (existing.count >= this.maxEventsPerGroup) {
      throw new AggregatorError(
        'GROUP_CAPACITY_EXCEEDED',
        `group "${group}" is bounded to ${this.maxEventsPerGroup} events`,
      );
    }
  }

  private stateFor(group: string): GroupState {
    let state = this.groups.get(group);
    if (state === undefined) {
      state = new GroupState();
      this.groups.set(group, state);
    }
    return state;
  }
}

function assertFinite(value: number): void {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new AggregatorError('INVALID_VALUE', `value must be a finite number, got ${value}`);
  }
}
