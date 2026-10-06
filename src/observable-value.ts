export type Listener<T> = (value: T, previous: T) => void;

export interface SubscribeOptions {
  /**
   * Higher priority listeners run earlier within a round.
   * Ties are broken by registration order. Defaults to 0.
   */
  priority?: number;
}

export type Unsubscribe = () => void;

interface ListenerEntry<T> {
  readonly listener: Listener<T>;
  readonly priority: number;
  readonly seq: number;
  active: boolean;
}

/**
 * An observable value with round-based notification.
 *
 * Semantics:
 * - Listeners run in rounds. Each round iterates over a fixed snapshot of the
 *   listeners registered when the round started, ordered by priority
 *   (descending) then registration order (ascending).
 * - A listener may call `set` (reentrancy). Nested updates never start a new
 *   round mid-round; they are merged and delivered in the next round, which
 *   uses a fresh snapshot (so listeners added during a round join the next
 *   round, never the current one).
 * - If the value returns to the value listeners were last notified about, the
 *   pending update is cancelled and no further round runs.
 * - Unsubscribing takes effect immediately: a listener removed before its
 *   turn in the current snapshot is skipped.
 * - Listener exceptions are collected and the round continues. After the
 *   value settles, `set` throws an `AggregateError` containing every
 *   collected exception.
 */
export class ObservableValue<T> {
  private listeners: ListenerEntry<T>[] = [];
  private nextSeq = 0;
  private flushing = false;
  private notifiedValue: T;
  private current: T;
  private errors: unknown[] = [];

  constructor(initial: T) {
    this.current = initial;
    this.notifiedValue = initial;
  }

  /** The current value, including updates pending in the active round. */
  get value(): T {
    return this.current;
  }

  get listenerCount(): number {
    return this.listeners.length;
  }

  subscribe(listener: Listener<T>, options: SubscribeOptions = {}): Unsubscribe {
    const entry: ListenerEntry<T> = {
      listener,
      priority: options.priority ?? 0,
      seq: this.nextSeq++,
      active: true,
    };
    // Keep the list sorted by (priority desc, seq asc) so each round's
    // snapshot is already in execution order.
    let index = this.listeners.length;
    while (
      index > 0 &&
      ObservableValue.compare(this.listeners[index - 1]!, entry) > 0
    ) {
      index--;
    }
    this.listeners.splice(index, 0, entry);

    let subscribed = true;
    return () => {
      if (!subscribed) return;
      subscribed = false;
      entry.active = false;
      const at = this.listeners.indexOf(entry);
      if (at >= 0) this.listeners.splice(at, 1);
    };
  }

  set(value: T): void {
    if (Object.is(value, this.current)) return;
    this.current = value;
    if (this.flushing) return; // nested update: merge into the next round
    this.flush();
  }

  private flush(): void {
    this.flushing = true;
    try {
      while (!Object.is(this.current, this.notifiedValue)) {
        const value = this.current;
        const previous = this.notifiedValue;
        // Commit before invoking listeners: nested sets only dirty the
        // state again if they move away from what we are notifying now.
        this.notifiedValue = value;
        const snapshot = this.listeners.slice();
        for (const entry of snapshot) {
          if (!entry.active) continue; // unsubscribed before its turn
          try {
            entry.listener(value, previous);
          } catch (error) {
            this.errors.push(error);
          }
        }
      }
    } finally {
      this.flushing = false;
    }
    if (this.errors.length > 0) {
      const errors = this.errors;
      this.errors = [];
      throw new AggregateError(
        errors,
        `${errors.length} listener(s) threw during notification`,
      );
    }
  }

  private static compare<T>(a: ListenerEntry<T>, b: ListenerEntry<T>): number {
    if (a.priority !== b.priority) return b.priority - a.priority;
    return a.seq - b.seq;
  }
}
