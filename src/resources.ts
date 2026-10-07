export class ResourceTracker {
  readonly live = new Set<Subscription>();
  totalCreated = 0;
  totalDisposed = 0;
  /** Ids of subscriptions disposed while not live (double-dispose). */
  readonly overDisposed: number[] = [];

  register(sub: Subscription): void {
    this.live.add(sub);
    this.totalCreated++;
  }

  release(sub: Subscription): void {
    if (!this.live.has(sub)) {
      this.overDisposed.push(sub.id);
      return;
    }
    this.live.delete(sub);
    this.totalDisposed++;
  }
}

let nextSubId = 1;

/** A resource acquired during prepare; must be disposed exactly once. */
export class Subscription {
  readonly id = nextSubId++;
  readonly label: string;
  disposeCount = 0;
  private readonly tracker: ResourceTracker;

  constructor(label: string, tracker: ResourceTracker) {
    this.label = label;
    this.tracker = tracker;
    tracker.register(this);
  }

  dispose(): void {
    this.disposeCount++;
    this.tracker.release(this);
  }
}
