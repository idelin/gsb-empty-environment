export type CancelFn = () => void;

export type ScheduleFn = (fn: () => void, delayMs: number) => CancelFn;

export type Loader<K, V> = (key: K, signal: AbortSignal) => Promise<V>;

export interface CoalescerOptions<K, V> {
  loader: Loader<K, V>;
  schedule?: ScheduleFn;
}

export interface LoadOptions {
  timeoutMs?: number;
  signal?: AbortSignal;
}

export class CoalescerTimeoutError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CoalescerTimeoutError';
  }
}

export class CoalescerAbortError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CoalescerAbortError';
  }
}

interface Caller<V> {
  resolve: (value: V) => void;
  reject: (error: unknown) => void;
  cancelTimer: CancelFn | undefined;
  signal: AbortSignal | undefined;
  onAbort: CancelFn | undefined;
}

interface Batch<K, V> {
  key: K;
  callers: Set<Caller<V>>;
  controller: AbortController;
  settled: boolean;
  aborted: boolean;
}

const defaultSchedule = (fn: () => void, delayMs: number): CancelFn => {
  const id = setTimeout(fn, delayMs);
  return () => clearTimeout(id);
};

export class RequestCoalescer<K, V> {
  private readonly loader: Loader<K, V>;
  private readonly schedule: ScheduleFn;
  private readonly batches: Map<K, Batch<K, V>> = new Map<K, Batch<K, V>>();

  constructor(options: CoalescerOptions<K, V>) {
    this.loader = options.loader;
    this.schedule = options.schedule === undefined ? defaultSchedule : options.schedule;
  }

  get pendingBatchCount(): number {
    return this.batches.size;
  }

  load(key: K, options?: LoadOptions): Promise<V> {
    const signal = options === undefined ? undefined : options.signal;
    const timeoutMs = options === undefined ? undefined : options.timeoutMs;
    if (signal !== undefined && signal.aborted) {
      return Promise.reject(new CoalescerAbortError('load() called with an already-aborted signal'));
    }
    return new Promise<V>((resolve, reject) => {
      const caller = {
        resolve,
        reject,
        cancelTimer: undefined,
        signal,
        onAbort: undefined,
      } as Caller<V>;
      let batch = this.batches.get(key);
      let isNew = false;
      if (batch === undefined) {
        batch = {
          key,
          callers: new Set<Caller<V>>(),
          controller: new AbortController(),
          settled: false,
          aborted: false,
        };
        this.batches.set(key, batch);
        isNew = true;
      }
      batch.callers.add(caller);
      this.armCaller(batch, caller, timeoutMs, signal);
      if (isNew) {
        this.invokeLoader(batch);
      }
    });
  }

  private armCaller(
    batch: Batch<K, V>,
    caller: Caller<V>,
    timeoutMs: number | undefined,
    signal: AbortSignal | undefined,
  ): void {
    if (timeoutMs !== undefined) {
      caller.cancelTimer = this.schedule(() => {
        this.removeCaller(batch, caller, new CoalescerTimeoutError('load() timed out after ' + timeoutMs + 'ms'));
      }, timeoutMs);
    }
    if (signal !== undefined) {
      const onAbort = () => {
        this.removeCaller(batch, caller, new CoalescerAbortError('load() aborted by the caller signal'));
      };
      caller.onAbort = onAbort;
      signal.addEventListener('abort', onAbort, { once: true });
    }
  }

  private invokeLoader(batch: Batch<K, V>): void {
    const promise = this.callLoader(batch);
    if (promise === undefined) {
      return;
    }
    promise.then(
      (value) => this.resolveBatch(batch, value),
      (error) => this.failBatch(batch, error),
    );
  }

  private callLoader(batch: Batch<K, V>): Promise<V> | undefined {
    try {
      return this.loader(batch.key, batch.controller.signal);
    } catch (error) {
      this.failBatch(batch, error);
      return undefined;
    }
  }

  private resolveBatch(batch: Batch<K, V>, value: V): void {
    if (batch.settled || batch.aborted) {
      return;
    }
    batch.settled = true;
    this.batches.delete(batch.key);
    for (const caller of batch.callers) {
      this.settleCaller(caller);
      caller.resolve(value);
    }
    batch.callers.clear();
  }

  private failBatch(batch: Batch<K, V>, error: unknown): void {
    if (batch.settled || batch.aborted) {
      return;
    }
    batch.settled = true;
    this.batches.delete(batch.key);
    for (const caller of batch.callers) {
      this.settleCaller(caller);
      caller.reject(error);
    }
    batch.callers.clear();
  }

  private removeCaller(batch: Batch<K, V>, caller: Caller<V>, error: unknown): void {
    if (!batch.callers.has(caller)) {
      return;
    }
    this.settleCaller(caller);
    batch.callers.delete(caller);
    caller.reject(error);
    if (batch.callers.size === 0 && !batch.settled && !batch.aborted) {
      this.teardownBatch(batch);
    }
  }

  private settleCaller(caller: Caller<V>): void {
    if (caller.cancelTimer !== undefined) {
      caller.cancelTimer();
      caller.cancelTimer = undefined;
    }
    if (caller.signal !== undefined && caller.onAbort !== undefined) {
      caller.signal.removeEventListener('abort', caller.onAbort);
      caller.onAbort = undefined;
    }
  }

  private teardownBatch(batch: Batch<K, V>): void {
    batch.aborted = true;
    if (this.batches.get(batch.key) === batch) {
      this.batches.delete(batch.key);
    }
    batch.controller.abort();
  }
}
