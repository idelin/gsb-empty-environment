import { MinHeap } from "./minHeap.ts";
import { OrderStatisticTree } from "./orderStatisticTree.ts";

export interface TimedEvent {
  /** Unique event identity; duplicates are distinguished by id. */
  id: string;
  /** Event time in milliseconds (any monotonic epoch works). */
  timestamp: number;
  /** Numeric payload the quantiles are computed over. */
  value: number;
}

function compareEvents(a: TimedEvent, b: TimedEvent): number {
  if (a.value !== b.value) return a.value - b.value;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * Exact quantiles over a sliding time window.
 *
 * The window holds events with `timestamp > now - windowMs` (a half-open
 * interval `(now - windowMs, now]`); anything older expires. `now` advances
 * explicitly via {@link advance} and implicitly to the newest timestamp seen
 * by {@link add}, so out-of-order inserts never move the window backwards.
 *
 * Internally an order-statistic treap keeps values sorted incrementally
 * (insert/remove/select in O(log n)) and a min-heap drives lazy expiry —
 * the window is never re-sorted wholesale.
 */
export class SlidingWindowQuantile {
  readonly windowMs: number;
  private readonly tree = new OrderStatisticTree<TimedEvent>(compareEvents);
  private readonly live = new Map<string, TimedEvent>();
  private readonly expiry = new MinHeap<TimedEvent>(
    (a, b) => a.timestamp < b.timestamp,
  );
  private now = Number.NEGATIVE_INFINITY;

  constructor(windowMs: number) {
    if (!(windowMs > 0)) {
      throw new RangeError("windowMs must be a positive number");
    }
    this.windowMs = windowMs;
  }

  /** Number of events currently inside the window. */
  get size(): number {
    return this.tree.size;
  }

  /** Current window right edge (`-Infinity` before any event). */
  get currentTime(): number {
    return this.now;
  }

  /**
   * Insert an event. Out-of-order timestamps are accepted; an event that is
   * already expired relative to the current window is dropped and `false`
   * is returned.
   */
  add(event: TimedEvent): boolean {
    if (this.live.has(event.id)) {
      throw new Error(`duplicate event id: ${event.id}`);
    }
    if (event.timestamp > this.now) {
      this.advance(event.timestamp);
    }
    if (event.timestamp <= this.now - this.windowMs) {
      return false;
    }
    const stored: TimedEvent = {
      id: event.id,
      timestamp: event.timestamp,
      value: event.value,
    };
    this.live.set(stored.id, stored);
    this.tree.insert(stored);
    this.expiry.push(stored);
    return true;
  }

  /** Retract a single live event by id. Returns false when not present. */
  retract(id: string): boolean {
    const event = this.live.get(id);
    if (event === undefined) return false;
    this.live.delete(id);
    this.tree.remove(event);
    return true;
  }

  /** Move the window right edge to `now`, expiring stale events. */
  advance(now: number): void {
    if (now < this.now) return;
    this.now = now;
    const cutoff = now - this.windowMs;
    for (;;) {
      const oldest = this.expiry.peek();
      if (oldest === undefined || oldest.timestamp > cutoff) break;
      this.expiry.pop();
      // Stale heap entries (already retracted) are skipped lazily.
      if (this.live.get(oldest.id) === oldest) {
        this.live.delete(oldest.id);
        this.tree.remove(oldest);
      }
    }
  }

  /**
   * Exact quantile: returns the value at rank `floor(q * (n - 1))` of the
   * window's sorted values (0-based). No interpolation.
   */
  quantile(q: number): number {
    const n = this.tree.size;
    if (n === 0) {
      throw new Error("quantile of an empty window");
    }
    if (!(q >= 0 && q <= 1)) {
      throw new RangeError(`q must be within [0, 1], got ${q}`);
    }
    const rank = Math.floor(q * (n - 1));
    return this.tree.select(rank).value;
  }
}
