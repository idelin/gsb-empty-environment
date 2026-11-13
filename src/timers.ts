export interface TimerEntry {
  readonly id: number;
  readonly dueAt: number;
  readonly intervalMs?: number;
  readonly callback: () => void;
  active: boolean;
}

export interface DueTimer {
  readonly id: number;
  readonly callback: () => void;
}

/**
 * Deterministic timer queue.
 *
 * `pause` records the freeze instant.  On `resume` every live deadline is
 * shifted by the frozen duration, preserving the remaining delay instead of
 * firing a burst of expired timers immediately after a BFCache restore.
 */
export class TimerRegistry {
  readonly #entries = new Map<number, TimerEntry>();
  #nextId = 1;
  #pausedAt: number | undefined;

  get paused(): boolean {
    return this.#pausedAt !== undefined;
  }

  get activeCount(): number {
    let count = 0;
    for (const entry of this.#entries.values()) {
      if (entry.active) {
        count += 1;
      }
    }
    return count;
  }

  schedule(delayMs: number, callback: () => void, now: number): number {
    const id = this.#nextId;
    this.#nextId += 1;
    this.#entries.set(id, {
      id,
      dueAt: now + Math.max(0, delayMs),
      callback,
      active: true,
    });
    return id;
  }

  scheduleInterval(
    intervalMs: number,
    callback: () => void,
    now: number,
  ): number {
    const id = this.#nextId;
    this.#nextId += 1;
    const period = Math.max(1, intervalMs);
    this.#entries.set(id, {
      id,
      dueAt: now + period,
      intervalMs: period,
      callback,
      active: true,
    });
    return id;
  }

  clear(id: number): boolean {
    const entry = this.#entries.get(id);
    if (!entry) {
      return false;
    }
    entry.active = false;
    this.#entries.delete(id);
    return true;
  }

  pause(at: number): void {
    if (this.#pausedAt === undefined) {
      this.#pausedAt = at;
    }
  }

  resume(at: number): void {
    if (this.#pausedAt === undefined) {
      return;
    }
    const frozenFor = Math.max(0, at - this.#pausedAt);
    for (const entry of this.#entries.values()) {
      (entry as { dueAt: number }).dueAt += frozenFor;
    }
    this.#pausedAt = undefined;
  }

  due(at: number): DueTimer[] {
    if (this.#pausedAt !== undefined) {
      return [];
    }
    const ready: TimerEntry[] = [];
    for (const entry of this.#entries.values()) {
      if (entry.active && entry.dueAt <= at) {
        ready.push(entry);
      }
    }
    ready.sort((left, right) => {
      if (left.dueAt !== right.dueAt) {
        return left.dueAt - right.dueAt;
      }
      return left.id - right.id;
    });
    const result: DueTimer[] = [];
    for (const entry of ready) {
      if (entry.intervalMs !== undefined) {
        (entry as { dueAt: number }).dueAt = at + entry.intervalMs;
      } else {
        entry.active = false;
        this.#entries.delete(entry.id);
      }
      result.push({ id: entry.id, callback: entry.callback });
    }
    return result;
  }

  clearAll(): void {
    this.#entries.clear();
  }

  remaining(id: number, now: number): number | undefined {
    const entry = this.#entries.get(id);
    if (!entry?.active) {
      return undefined;
    }
    return Math.max(0, entry.dueAt - now);
  }
}
