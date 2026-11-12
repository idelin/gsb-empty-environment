export interface Clock {
  now(): number;
}

interface PendingTask {
  id: number;
  runAt: number;
  fn: () => void;
}

/**
 * Deterministic fake clock with a macrotask queue (timer tasks) and a
 * microtask queue. Time only moves when the test advances it, so microtasks
 * always run at the same timestamp as the task that queued them — which is
 * exactly why microtasks can never renew a time-bounded activation.
 */
export class VirtualClock implements Clock {
  #time = 0;
  #nextId = 1;
  #tasks: PendingTask[] = [];
  #microtasks: Array<() => void> = [];

  now(): number {
    return this.#time;
  }

  setMacrotask(fn: () => void, delayMs = 0): number {
    const id = this.#nextId++;
    this.#tasks.push({ id, runAt: this.#time + Math.max(0, delayMs), fn });
    return id;
  }

  setMicrotask(fn: () => void): void {
    this.#microtasks.push(fn);
  }

  #drainMicrotasks(): void {
    while (this.#microtasks.length > 0) {
      const queue = this.#microtasks;
      this.#microtasks = [];
      for (const fn of queue) {
        fn();
      }
    }
  }

  advance(ms: number): void {
    this.advanceTo(this.#time + ms);
  }

  advanceTo(target: number): void {
    if (target < this.#time) {
      throw new Error('virtual clock cannot move backwards');
    }
    for (;;) {
      const due = this.#tasks
        .filter((task) => task.runAt <= target)
        .sort((a, b) => a.runAt - b.runAt || a.id - b.id);
      if (due.length === 0) {
        break;
      }
      const next = due[0];
      this.#time = next.runAt;
      this.#tasks = this.#tasks.filter((task) => task.id !== next.id);
      next.fn();
      this.#drainMicrotasks();
    }
    this.#time = target;
    this.#drainMicrotasks();
  }

  pendingTaskCount(): number {
    return this.#tasks.length;
  }
}
