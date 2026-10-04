/**
 * Numbered, trackable elements flowing through the pipeline.
 *
 * Every element gets a monotonically increasing id and must be released
 * exactly once. Ownership rules:
 *  - the creator owns an element until `send()` completes;
 *  - once sent, the channel owns it;
 *  - once `receive()` returns it, the receiving stage owns it and must
 *    release it (after processing, or immediately when aborting).
 */

export interface ReleaseRecord {
  readonly id: number;
  readonly by: string;
  readonly seq: number;
}

export class ElementTracker {
  private nextId = 0;
  private readonly live = new Map<number, TrackedElement<unknown>>();
  readonly releases: ReleaseRecord[] = [];

  create<T>(value: T): TrackedElement<T> {
    const el = new TrackedElement<T>(this, this.nextId, value);
    this.nextId += 1;
    this.live.set(el.id, el as TrackedElement<unknown>);
    return el;
  }

  release<T>(el: TrackedElement<T>, by: string): void {
    if (el.released) {
      throw new Error(`element #${el.id} released twice (now by "${by}")`);
    }
    el.released = true;
    this.live.delete(el.id);
    this.releases.push({ id: el.id, by, seq: this.releases.length });
  }

  get createdCount(): number {
    return this.nextId;
  }

  get liveCount(): number {
    return this.live.size;
  }

  get liveIds(): number[] {
    return [...this.live.keys()];
  }

  assertAllReleased(): void {
    if (this.live.size > 0) {
      throw new Error(`leaked elements (never released): ${this.liveIds.join(", ")}`);
    }
  }
}

export class TrackedElement<T> {
  readonly tracker: ElementTracker;
  readonly id: number;
  readonly value: T;
  released = false;

  constructor(tracker: ElementTracker, id: number, value: T) {
    this.tracker = tracker;
    this.id = id;
    this.value = value;
  }

  release(by: string): void {
    this.tracker.release(this, by);
  }

  toString(): string {
    return `Element#${this.id}(${String(this.value)})`;
  }
}
