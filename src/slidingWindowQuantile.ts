import { OrderStatisticSkipList } from "./skipList.ts";

export interface WindowEvent {
  id: string;
  /** Event time in milliseconds. */
  timestamp: number;
  value: number;
}

/**
 * Exact quantiles over a sliding time window.
 *
 * Two order-statistics skip lists index live events: one ordered by
 * (value, id) for O(log n) rank selection, one ordered by (timestamp, id)
 * for O(log n) expiry of the oldest events. A Map by id supports exact
 * single-event retraction. No bulk re-sorting ever happens; every
 * mutation is a single O(log n) structural update.
 *
 * Window semantics: an event is live while timestamp > now - windowMs,
 * i.e. the window is the half-open interval (now - windowMs, now].
 * `now` only moves forward; out-of-order (late) events are accepted as
 * long as they have not already expired.
 */
export class SlidingWindowQuantile {
  private readonly windowMs: number;
  private readonly byValue = new OrderStatisticSkipList();
  private readonly byTime = new OrderStatisticSkipList();
  private readonly events = new Map<string, WindowEvent>();
  private now: number | null = null;

  constructor(windowMs = 10_000) {
    if (!(windowMs > 0)) throw new RangeError("windowMs must be positive");
    this.windowMs = windowMs;
  }

  get size(): number {
    return this.events.size;
  }

  get currentTime(): number | null {
    return this.now;
  }

  add(event: WindowEvent): void {
    if (this.events.has(event.id)) {
      throw new Error(`duplicate event id: ${event.id}`);
    }
    this.advance(event.timestamp);
    if (this.now !== null && event.timestamp <= this.now - this.windowMs) {
      throw new Error(`event ${event.id} arrived already expired`);
    }
    this.events.set(event.id, event);
    this.byValue.insert([event.value, event.id]);
    this.byTime.insert([event.timestamp, event.id]);
  }

  /** Retracts a single live event by id. Returns false if unknown. */
  retract(id: string): boolean {
    const event = this.events.get(id);
    if (event === undefined) return false;
    this.events.delete(id);
    this.byValue.remove([event.value, event.id]);
    this.byTime.remove([event.timestamp, event.id]);
    return true;
  }

  /** Moves the window forward and expires events outside it. */
  advance(now: number): void {
    if (this.now === null || now > this.now) this.now = now;
    this.expire();
  }

  private expire(): void {
    if (this.now === null) return;
    const cutoff = this.now - this.windowMs;
    for (;;) {
      const oldest = this.byTime.first();
      if (oldest === null || oldest[0] > cutoff) break;
      this.retract(oldest[1]);
    }
  }

  /**
   * Exact quantile at rank floor(q * (n - 1)) of the sorted live values.
   * Optionally advances the window to `now` first.
   * Returns undefined when the window is empty.
   */
  quantile(q: number, now?: number): number | undefined {
    if (!(q >= 0 && q <= 1)) throw new RangeError("q must be within [0, 1]");
    if (now !== undefined) this.advance(now);
    const n = this.events.size;
    if (n === 0) return undefined;
    const k = Math.floor(q * (n - 1));
    const key = this.byValue.select(k + 1);
    return key === null ? undefined : key[0];
  }

  /** Sorted snapshot of live values (diagnostics only). */
  values(): number[] {
    return this.byValue.toArray().map((key) => key[0]);
  }
}
