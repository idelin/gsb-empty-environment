/**
 * KeyedExecutor — keyed job executor.
 *
 * Semantics:
 * - Jobs submitted with the same key run strictly serially, in FIFO order.
 * - Jobs with different keys run concurrently, bounded by a shared global
 *   concurrency limit. Ready keys are scheduled in FIFO order.
 * - A failed job never blocks its key's successors; the rejection is
 *   delivered only to that job's handle.
 * - Queued jobs can be cancelled; running jobs cannot.
 * - A key's scheduling state is released synchronously once the key becomes
 *   idle (no running job, empty queue), so memory does not grow with the
 *   number of distinct keys.
 * - close() cancels every queued job, rejects new submissions (including
 *   recursive submissions from still-running jobs) with ExecutorClosedError,
 *   and resolves once all running jobs have settled.
 */

export class ExecutorClosedError extends Error {
  constructor(message = 'executor is closed') {
    super(message);
    this.name = 'ExecutorClosedError';
  }
}

export class JobCancelledError extends Error {
  constructor(message = 'job was cancelled') {
    super(message);
    this.name = 'JobCancelledError';
  }
}

export type JobFn<T> = () => T | Promise<T>;

export type JobState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';

export interface JobHandle<T> {
  readonly id: number;
  readonly key: string;
  /** Live view of the underlying job state. */
  readonly state: JobState;
  /** Resolves/rejects with the job's outcome, or rejects on cancellation/close. */
  readonly promise: Promise<T>;
  /** Cancel a queued job. Returns false if the job already left the queue. */
  cancel(): boolean;
}

interface JobRecord<T> {
  id: number;
  key: string;
  fn: JobFn<T>;
  state: JobState;
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
  reject: (reason: unknown) => void;
}

interface KeyLane {
  queue: Array<JobRecord<unknown>>;
  /** True while one job of this key is executing. */
  running: boolean;
  /** True while this key occupies a slot in the ready queue. */
  enqueued: boolean;
}

export interface KeyedExecutorOptions {
  /** Maximum number of jobs executing concurrently across all keys. */
  concurrency?: number;
}

export class KeyedExecutor {
  private readonly maxConcurrency: number;
  private readonly lanes = new Map<string, KeyLane>();
  private readonly readyKeys: string[] = [];
  private running = 0;
  private closed = false;
  private closing: Promise<void> | null = null;
  private notifyIdle: (() => void) | null = null;
  private nextId = 1;

  constructor(options: KeyedExecutorOptions = {}) {
    const concurrency = options.concurrency ?? 4;
    if (!Number.isInteger(concurrency) || concurrency < 1) {
      throw new RangeError(`concurrency must be a positive integer, got ${concurrency}`);
    }
    this.maxConcurrency = concurrency;
  }

  get isClosed(): boolean {
    return this.closed;
  }

  /** Number of jobs currently executing. */
  get runningCount(): number {
    return this.running;
  }

  /** Number of keys with live scheduling state (running job or non-empty queue). */
  get trackedKeyCount(): number {
    return this.lanes.size;
  }

  hasKey(key: string): boolean {
    return this.lanes.has(key);
  }

  queuedCount(key: string): number {
    const lane = this.lanes.get(key);
    return lane === undefined ? 0 : lane.queue.length;
  }

  submit<T>(key: string, fn: JobFn<T>): JobHandle<T> {
    let record!: JobRecord<T>;
    const promise = new Promise<T>((resolve, reject) => {
      record = {
        id: this.nextId++,
        key,
        fn,
        state: 'queued',
        promise: null as unknown as Promise<T>,
        resolve,
        reject,
      };
    });
    record.promise = promise;
    // A job nobody awaits must not crash the process with an unhandled
    // rejection; awaiting handle.promise still surfaces the failure.
    promise.catch(() => undefined);

    if (this.closed) {
      record.state = 'failed';
      record.reject(new ExecutorClosedError(`cannot submit job ${record.id} to key "${key}": executor is closed`));
      return this.handleFor(record);
    }

    let lane = this.lanes.get(key);
    if (lane === undefined) {
      lane = { queue: [], running: false, enqueued: false };
      this.lanes.set(key, lane);
    }
    lane.queue.push(record as JobRecord<unknown>);
    if (!lane.running && !lane.enqueued) {
      lane.enqueued = true;
      this.readyKeys.push(key);
    }
    this.pump();
    return this.handleFor(record);
  }

  /**
   * Cancel all queued jobs and stop accepting new ones, then resolve once
   * every running job has settled. Idempotent: always returns the same promise.
   */
  close(): Promise<void> {
    if (this.closing !== null) {
      return this.closing;
    }
    this.closed = true;
    this.readyKeys.length = 0;
    for (const [key, lane] of Array.from(this.lanes)) {
      const cancelled = lane.queue.splice(0);
      for (const record of cancelled) {
        record.state = 'cancelled';
        record.reject(new JobCancelledError(`job ${record.id} on key "${key}" cancelled: executor is closed`));
      }
      this.releaseLaneIfIdle(key, lane);
    }
    this.closing = new Promise<void>((resolve) => {
      if (this.running === 0) {
        resolve();
      } else {
        this.notifyIdle = resolve;
      }
    });
    return this.closing;
  }

  private handleFor<T>(record: JobRecord<T>): JobHandle<T> {
    return {
      id: record.id,
      key: record.key,
      get state() {
        return record.state;
      },
      promise: record.promise,
      cancel: () => this.cancelRecord(record as JobRecord<unknown>),
    };
  }

  private cancelRecord(record: JobRecord<unknown>): boolean {
    if (record.state !== 'queued') {
      return false;
    }
    record.state = 'cancelled';
    const lane = this.lanes.get(record.key);
    if (lane !== undefined) {
      const index = lane.queue.indexOf(record);
      if (index >= 0) {
        lane.queue.splice(index, 1);
      }
      this.releaseLaneIfIdle(record.key, lane);
    }
    record.reject(new JobCancelledError(`job ${record.id} on key "${record.key}" was cancelled`));
    return true;
  }

  private releaseLaneIfIdle(key: string, lane: KeyLane): void {
    if (!lane.running && lane.queue.length === 0 && this.lanes.get(key) === lane) {
      this.lanes.delete(key);
    }
  }

  private pump(): void {
    if (this.closed) {
      return;
    }
    while (this.running < this.maxConcurrency && this.readyKeys.length > 0) {
      const key = this.readyKeys.shift() as string;
      const lane = this.lanes.get(key);
      if (lane === undefined) {
        continue; // stale entry: lane was released after being enqueued
      }
      lane.enqueued = false;
      if (lane.running || lane.queue.length === 0) {
        continue;
      }
      const record = lane.queue.shift() as JobRecord<unknown>;
      lane.running = true;
      this.running += 1;
      this.runRecord(key, lane, record);
    }
  }

  private runRecord(key: string, lane: KeyLane, record: JobRecord<unknown>): void {
    record.state = 'running';
    let result: Promise<unknown>;
    try {
      // fn() may synchronously submit more jobs (same key included) or even
      // call close(); lane.running and this.running are already updated, so
      // those re-entrant calls see a consistent executor.
      result = Promise.resolve(record.fn());
    } catch (error) {
      result = Promise.reject(error);
    }
    result.then(
      (value) => this.settle(key, lane, record, 'succeeded', value),
      (error) => this.settle(key, lane, record, 'failed', error),
    );
  }

  private settle(
    key: string,
    lane: KeyLane,
    record: JobRecord<unknown>,
    state: 'succeeded' | 'failed',
    payload: unknown,
  ): void {
    lane.running = false;
    this.running -= 1;
    record.state = state;
    if (state === 'succeeded') {
      record.resolve(payload);
    } else {
      record.reject(payload);
    }

    if (lane.queue.length > 0) {
      if (!lane.enqueued) {
        lane.enqueued = true;
        this.readyKeys.push(key);
      }
    } else if (this.lanes.get(key) === lane) {
      // Idle key: release its state synchronously, before any user callback
      // attached to record.promise runs. A task submitted from such a
      // callback therefore starts from a fresh lane instead of observing
      // half-cleaned state.
      this.lanes.delete(key);
    }

    this.pump();

    if (this.notifyIdle !== null && this.running === 0) {
      const notify = this.notifyIdle;
      this.notifyIdle = null;
      notify();
    }
  }
}
