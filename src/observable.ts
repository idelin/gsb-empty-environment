export type Listener<T> = (value: T, previous: T) => void;

export type Unsubscribe = () => void;

/**
 * Thrown by `set` after all notification rounds finish when one or more
 * listeners raised. Remaining listeners still ran; every failure is
 * collected in `errors` in the order it occurred.
 */
export class ObserverNotificationError extends Error {
  readonly errors: unknown[];

  constructor(errors: unknown[]) {
    super(`${errors.length} listener(s) threw during notification`);
    this.name = "ObserverNotificationError";
    this.errors = errors;
  }
}

interface ListenerEntry<T> {
  id: number;
  priority: number;
  listener: Listener<T>;
  active: boolean;
}

/**
 * Observable value with re-entrant, round-based notification.
 *
 * Semantics:
 * - Listeners run ordered by ascending `priority`, ties broken by
 *   registration order.
 * - Each round iterates a fixed snapshot taken at the start of the round;
 *   listeners subscribed mid-round join from the next round on.
 * - Unsubscribing takes effect immediately: a listener removed before its
 *   turn in the current snapshot is skipped.
 * - `set` called from inside a listener never recurses; nested writes are
 *   merged and only the final value is dispatched in the next round.
 * - If a nested write restores the value that was current before the
 *   round, no further round is scheduled.
 * - Listener exceptions are collected and notification continues; after
 *   the last round `set` throws an `ObserverNotificationError`.
 */
export class Observable<T> {
  private current: T;
  private lastNotified: T;
  private listeners: ListenerEntry<T>[] = [];
  private nextId = 0;
  private notifying = false;

  constructor(initial: T) {
    this.current = initial;
    this.lastNotified = initial;
  }

  get(): T {
    return this.current;
  }

  subscribe(listener: Listener<T>, priority = 0): Unsubscribe {
    const entry: ListenerEntry<T> = {
      id: this.nextId++,
      priority,
      listener,
      active: true,
    };
    this.listeners.push(entry);
    return () => {
      if (!entry.active) return;
      entry.active = false;
      const index = this.listeners.indexOf(entry);
      if (index !== -1) this.listeners.splice(index, 1);
    };
  }

  listenerCount(): number {
    return this.listeners.length;
  }

  set(value: T): void {
    if (!this.notifying && Object.is(value, this.current)) return;
    this.current = value;
    if (this.notifying) return;
    this.flush();
  }

  private flush(): void {
    const errors: unknown[] = [];
    this.notifying = true;
    try {
      for (;;) {
        const previous = this.lastNotified;
        const value = this.current;
        if (Object.is(value, previous)) break;
        const snapshot = [...this.listeners].sort(
          (a, b) => a.priority - b.priority || a.id - b.id,
        );
        for (const entry of snapshot) {
          if (!entry.active) continue;
          try {
            entry.listener(value, previous);
          } catch (error) {
            errors.push(error);
          }
        }
        this.lastNotified = value;
        // A nested write restored the value listeners saw before this
        // round: the round is moot, so no further round is scheduled.
        if (Object.is(this.current, previous)) {
          this.lastNotified = previous;
          break;
        }
      }
    } finally {
      this.notifying = false;
    }
    if (errors.length > 0) throw new ObserverNotificationError(errors);
  }
}
