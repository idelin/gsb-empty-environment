import type { MaybePromise, RunOptions } from "./types.ts";

export type TaskLane = "control" | "document";

export interface ScheduledTask {
  readonly id: number;
  readonly lane: TaskLane;
  readonly label: string;
  readonly enqueuedAt: number;
  readonly run: () => MaybePromise<void>;
  cancelled: boolean;
}

export interface TaskHandle {
  readonly id: number;
  readonly lane: TaskLane;
  readonly label: string;
  cancel(): void;
}

export interface TaskRunResult {
  readonly task: ScheduledTask;
  readonly error?: unknown;
}

/**
 * A deterministic two-lane event loop.
 *
 * Control tasks (auth epoch updates, policy updates, pageshow validation)
 * always run before document tasks.  This is a security property rather than
 * a scheduling preference: a network response that arrived before a logout
 * event but whose callback is still queued must not run until the host has
 * processed the revocation.
 *
 * Within each lane tasks run in enqueue order.  No wall-clock or native
 * microtask timing is involved.
 */
export class DeterministicEventLoop {
  readonly #control: ScheduledTask[] = [];
  readonly #document: ScheduledTask[] = [];
  #nextId = 1;
  #tasksRun = 0;
  #lastError: unknown;
  #controlDriver: Promise<number> | undefined;
  #allDriver: Promise<number> | undefined;
  #singleDriver: Promise<unknown> | undefined;

  get tasksRun(): number {
    return this.#tasksRun;
  }

  get lastError(): unknown {
    return this.#lastError;
  }

  get size(): number {
    return this.#control.length + this.#document.length;
  }

  get controlSize(): number {
    this.#discardCancelledHead();
    return this.#control.length;
  }

  get documentSize(): number {
    this.#discardCancelledHead();
    return this.#document.length;
  }

  enqueue(
    lane: TaskLane,
    label: string,
    run: () => MaybePromise<void>,
  ): TaskHandle {
    const task: ScheduledTask = {
      id: this.#nextId,
      lane,
      label,
      enqueuedAt: this.#nextId,
      run,
      cancelled: false,
    };
    this.#nextId += 1;
    if (lane === "control") {
      this.#control.push(task);
    } else {
      this.#document.push(task);
    }
    return {
      id: task.id,
      lane,
      label,
      cancel: () => {
        task.cancelled = true;
      },
    };
  }

  cancelWhere(predicate: (task: ScheduledTask) => boolean): number {
    let cancelled = 0;
    for (const task of [...this.#control, ...this.#document]) {
      if (!task.cancelled && predicate(task)) {
        task.cancelled = true;
        cancelled += 1;
      }
    }
    return cancelled;
  }

  peek(): ScheduledTask | undefined {
    this.#discardCancelledHead();
    return this.#control[0] ?? this.#document[0];
  }

  pending(): readonly ScheduledTask[] {
    return [...this.#control, ...this.#document].filter(
      (task) => !task.cancelled,
    );
  }

  async runNext(): Promise<TaskRunResult | undefined> {
    if (this.#controlDriver || this.#allDriver || this.#singleDriver) {
      await this.#waitForDrivers();
    }
    return this.#runExclusive(() => this.#runNextUnlocked());
  }

  async #runNextUnlocked(): Promise<TaskRunResult | undefined> {
    const task = this.#dequeue();
    if (!task) {
      return undefined;
    }
    try {
      await task.run();
      this.#tasksRun += 1;
      return { task };
    } catch (error) {
      this.#tasksRun += 1;
      this.#lastError = error;
      return { task, error };
    }
  }

  async runUntilIdle(options: RunOptions = {}): Promise<number> {
    if (this.#controlDriver || this.#allDriver || this.#singleDriver) {
      await this.#waitForDrivers();
    }
    if (this.size === 0) {
      return 0;
    }
    const driver = this.#drainAll(options);
    this.#allDriver = driver;
    try {
      return await driver;
    } finally {
      if (this.#allDriver === driver) {
        this.#allDriver = undefined;
      }
    }
  }

  async #drainAll(options: RunOptions): Promise<number> {
    const maxTasks = options.maxTasks ?? 10000;
    let ran = 0;
    while (this.size > 0) {
      if (ran >= maxTasks) {
        throw new Error(
          `event loop did not become idle within ${maxTasks} tasks`,
        );
      }
      await this.#runNextUnlocked();
      ran += 1;
    }
    return ran;
  }

  /**
   * Run control tasks until only document work remains.
   *
   * A control task may enqueue document work (for example, flushing deferred
   * responses after a successful resume).  That work is intentionally left
   * queued for the caller, so validation and document execution remain
   * separate observable phases.
   */
  async runControlUntilIdle(maxTasks = 1000): Promise<number> {
    if (this.#controlDriver || this.#allDriver || this.#singleDriver) {
      await this.#waitForDrivers();
    }
    if (this.controlSize === 0) {
      return 0;
    }
    const driver = this.#drainControl(maxTasks);
    this.#controlDriver = driver;
    try {
      return await driver;
    } finally {
      if (this.#controlDriver === driver) {
        this.#controlDriver = undefined;
      }
    }
  }

  async #drainControl(maxTasks: number): Promise<number> {
    let ran = 0;
    while (this.controlSize > 0) {
      if (ran >= maxTasks) {
        throw new Error(
          `control lane did not become idle within ${maxTasks} tasks`,
        );
      }
      await this.#runNextControlUnlocked();
      ran += 1;
    }
    return ran;
  }

  async runNextControl(): Promise<TaskRunResult | undefined> {
    if (this.#controlDriver || this.#allDriver || this.#singleDriver) {
      await this.#waitForDrivers();
    }
    return this.#runExclusive(() => this.#runNextControlUnlocked());
  }

  async #runNextControlUnlocked(): Promise<TaskRunResult | undefined> {
    this.#discardCancelledHead();
    const task = this.#control.shift();
    if (!task || task.cancelled) {
      return undefined;
    }
    try {
      await task.run();
      this.#tasksRun += 1;
      return { task };
    } catch (error) {
      this.#tasksRun += 1;
      this.#lastError = error;
      return { task, error };
    }
  }

  async #waitForDrivers(): Promise<void> {
    while (this.#controlDriver || this.#allDriver || this.#singleDriver) {
      if (this.#controlDriver) {
        try {
          await this.#controlDriver;
        } catch {
          // The driver records its own error.
        }
      } else if (this.#allDriver) {
        try {
          await this.#allDriver;
        } catch {
          // The driver records its own error.
        }
      } else if (this.#singleDriver) {
        try {
          await this.#singleDriver;
        } catch {
          // The driver records its own error.
        }
      }
    }
  }

  async #runExclusive<T>(
    run: () => Promise<T>,
  ): Promise<T> {
    while (this.#singleDriver) {
      try {
        await this.#singleDriver;
      } catch {
        // The previous single-task driver recorded its own error.
      }
    }
    const driver = run();
    this.#singleDriver = driver;
    try {
      return await driver;
    } finally {
      if (this.#singleDriver === driver) {
        this.#singleDriver = undefined;
      }
    }
  }

  #dequeue(): ScheduledTask | undefined {
    this.#discardCancelledHead();
    const task = this.#control.shift() ?? this.#document.shift();
    if (task && !task.cancelled) {
      return task;
    }
    return undefined;
  }

  #discardCancelledHead(): void {
    while (this.#control[0]?.cancelled) {
      this.#control.shift();
    }
    while (this.#document[0]?.cancelled) {
      this.#document.shift();
    }
  }
}
