/**
 * Deterministic, manually-pumped scheduler.
 *
 * Async work (font loads, shaping, measurement) is enqueued as jobs and only
 * executes when the caller pumps the queue. Tests use it to control response
 * ordering precisely: stale replies, out-of-order font loads and interleaved
 * hot reloads are all reproducible without timers.
 */

export interface ScheduledTask<T> {
  readonly id: number;
  readonly label: string;
  readonly promise: Promise<T>;
  /** true once the job has been executed by a pump */
  readonly started: boolean;
}

type Job = {
  id: number;
  label: string;
  run: () => Promise<unknown>;
};

export class ControllableScheduler {
  private queue: Job[] = [];
  private nextId = 1;
  private executed: string[] = [];

  get pendingCount(): number {
    return this.queue.length;
  }

  /** Labels of jobs in the order they were executed — for assertions. */
  get executionLog(): readonly string[] {
    return this.executed;
  }

  enqueue<T>(label: string, run: () => Promise<T> | T): ScheduledTask<T> {
    const id = this.nextId++;
    let resolvePromise!: (value: T) => void;
    let rejectPromise!: (reason: unknown) => void;
    const promise = new Promise<T>((resolve, reject) => {
      resolvePromise = resolve;
      rejectPromise = reject;
    });
    const task: ScheduledTask<T> = {
      id,
      label,
      promise,
      get started() {
        return started;
      },
    };
    let started = false;
    this.queue.push({
      id,
      label,
      run: async () => {
        started = true;
        this.executed.push(label);
        try {
          resolvePromise(await run());
        } catch (error) {
          rejectPromise(error);
        }
      },
    });
    return task;
  }

  /**
   * Start the next `n` queued jobs in FIFO order. Jobs are *started*, not
   * awaited to completion: a job may suspend on work it just enqueued (e.g.
   * a measurement waiting for its font load), so awaiting completion here
   * would deadlock. After starting each job the microtask queue is flushed
   * so dependent continuations can enqueue follow-up jobs.
   */
  async pump(n = 1): Promise<void> {
    for (let i = 0; i < n; i += 1) {
      const job = this.queue.shift();
      if (!job) return;
      void job.run();
      await new Promise((resolve) => setImmediate(resolve));
    }
  }

  async pumpAll(): Promise<void> {
    while (this.queue.length > 0) {
      await this.pump(1);
    }
  }
}
