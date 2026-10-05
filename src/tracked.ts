export interface HandoffRecord {
  from: string;
  to: string;
  itemId: number;
}

export interface ReleaseRecord {
  itemId: number;
  by: string;
}

/** An element with a unique sequence number, tracked across handoffs. */
export class TrackedItem<T> {
  released = false;
  releasedBy: string | null = null;
  readonly id: number;
  value: T;

  constructor(id: number, value: T) {
    this.id = id;
    this.value = value;
  }
}

/**
 * Tracks every numbered element: who handed it to whom, and who
 * released it. Double release throws immediately; `assertNoLeaks`
 * verifies every produced element was released exactly once.
 */
export class Tracker {
  private items = new Map<number, TrackedItem<unknown>>();
  private seq = 0;
  readonly handoffs: HandoffRecord[] = [];
  readonly releases: ReleaseRecord[] = [];

  produce<T>(value: T): TrackedItem<T> {
    this.seq += 1;
    const item = new TrackedItem<T>(this.seq, value);
    this.items.set(item.id, item as TrackedItem<unknown>);
    return item;
  }

  handoff(from: string, to: string, item: TrackedItem<unknown>): void {
    this.handoffs.push({ from, to, itemId: item.id });
  }

  release(by: string, item: TrackedItem<unknown>): void {
    if (item.released) {
      throw new Error(
        `double release of item #${item.id} by "${by}" ` +
          `(already released by "${item.releasedBy}")`,
      );
    }
    item.released = true;
    item.releasedBy = by;
    this.releases.push({ itemId: item.id, by });
  }

  get producedCount(): number {
    return this.items.size;
  }

  leakedIds(): number[] {
    const leaked: number[] = [];
    for (const [id, item] of this.items) {
      if (!item.released) leaked.push(id);
    }
    return leaked.sort((a, b) => a - b);
  }

  assertNoLeaks(): void {
    const leaked = this.leakedIds();
    if (leaked.length > 0) {
      throw new Error(`leaked items (never released): ${leaked.join(', ')}`);
    }
  }
}
