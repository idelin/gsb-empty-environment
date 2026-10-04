import { SystemScheduler, type Scheduler } from "./scheduler.ts";

export class TimeoutError extends Error {
  constructor(message = "request deadline exceeded") {
    super(message);
    this.name = "TimeoutError";
  }
}

export class CoalescerAbortError extends Error {
  constructor(message = "request was cancelled") {
    super(message);
    this.name = "CoalescerAbortError";
  }
}

export interface RequestOptions {
  /** Per-caller deadline in milliseconds from now. Omit for no deadline. */
  deadlineMs?: number;
  /** Optional caller-side cancellation. */
  signal?: AbortSignal;
}

export type Loader<K, V> = (key: K, signal: AbortSignal) => Promise<V>;

interface Participant<V> {
  resolve: (value: V) => void;
  reject: (error: unknown) => void;
  timerHandle: unknown;
  removeSignalListener: (() => void) | null;
}

interface Batch<K, V> {
  readonly key: K;
  readonly controller: AbortController;
  readonly participants: Set<Participant<V>>;
  settled: boolean;
}

/**
 * Deduplicates concurrent loads by key.
 *
 * - The first request for a key starts a batch and invokes the loader once;
 *   later requests for the same key join the in-flight batch.
 * - Every caller keeps its own deadline/cancellation: one caller leaving
 *   rejects only that caller and never cancels the shared load.
 * - When the last participant leaves, the batch is torn down and the loader's
 *   AbortSignal is aborted.
 * - A batch that settled or was torn down is detached from the registry
 *   before any user code runs, so a late loader result can never resolve a
 *   newer batch for the same key, and abort listeners may safely re-enter
 *   `request`.
 */
export class RequestCoalescer<K, V> {
  private readonly batches = new Map<K, Batch<K, V>>();
  private readonly loader: Loader<K, V>;
  private readonly scheduler: Scheduler;

  constructor(loader: Loader<K, V>, scheduler: Scheduler = new SystemScheduler()) {
    this.loader = loader;
    this.scheduler = scheduler;
  }

  /** Number of in-flight batches (for observability/tests). */
  get inFlightCount(): number {
    return this.batches.size;
  }

  request(key: K, options: RequestOptions = {}): Promise<V> {
    if (options.signal?.aborted) {
      return Promise.reject(new CoalescerAbortError());
    }
    let batch = this.batches.get(key);
    if (batch === undefined) {
      batch = this.startBatch(key);
    }
    return this.join(batch, options);
  }

  private startBatch(key: K): Batch<K, V> {
    const batch: Batch<K, V> = {
      key,
      controller: new AbortController(),
      participants: new Set(),
      settled: false,
    };
    this.batches.set(key, batch);

    let result: Promise<V>;
    try {
      result = this.loader(key, batch.controller.signal);
    } catch (error) {
      // A loader that throws synchronously must not take the coalescer down
      // with it: detach the batch immediately and reject asynchronously.
      result = Promise.reject(error);
    }

    result.then(
      (value) => this.settleBatch(batch, null, value),
      (error) => this.settleBatch(batch, error, undefined),
    );
    return batch;
  }

  private join(batch: Batch<K, V>, options: RequestOptions): Promise<V> {
    const participant: Participant<V> = {
      resolve: () => {},
      reject: () => {},
      timerHandle: null,
      removeSignalListener: null,
    };

    const promise = new Promise<V>((resolve, reject) => {
      participant.resolve = resolve;
      participant.reject = reject;
    });

    if (options.deadlineMs !== undefined) {
      participant.timerHandle = this.scheduler.setTimeout(() => {
        this.leave(batch, participant, new TimeoutError());
      }, options.deadlineMs);
    }

    if (options.signal) {
      const signal = options.signal;
      const onAbort = () => {
        this.leave(batch, participant, new CoalescerAbortError());
      };
      signal.addEventListener("abort", onAbort, { once: true });
      participant.removeSignalListener = () => {
        signal.removeEventListener("abort", onAbort);
      };
    }

    batch.participants.add(participant);
    return promise;
  }

  private leave(batch: Batch<K, V>, participant: Participant<V>, error: unknown): void {
    if (!batch.participants.delete(participant)) return;
    this.releaseParticipant(participant);
    participant.reject(error);
    if (batch.participants.size === 0) {
      this.teardown(batch);
    }
  }

  private settleBatch(batch: Batch<K, V>, error: unknown, value: V | undefined): void {
    // Late results from an aborted/superseded batch are dropped here.
    if (batch.settled) return;
    batch.settled = true;
    this.detach(batch);
    for (const participant of batch.participants) {
      this.releaseParticipant(participant);
      if (error === null) {
        participant.resolve(value as V);
      } else {
        participant.reject(error);
      }
    }
    batch.participants.clear();
  }

  private teardown(batch: Batch<K, V>): void {
    if (batch.settled) return;
    batch.settled = true;
    // Detach before aborting so a re-entrant `request` from an abort
    // listener starts a fresh batch instead of touching this one.
    this.detach(batch);
    batch.controller.abort();
  }

  private detach(batch: Batch<K, V>): void {
    if (this.batches.get(batch.key) === batch) {
      this.batches.delete(batch.key);
    }
  }

  private releaseParticipant(participant: Participant<V>): void {
    if (participant.timerHandle !== null) {
      this.scheduler.clearTimeout(participant.timerHandle);
      participant.timerHandle = null;
    }
    if (participant.removeSignalListener !== null) {
      participant.removeSignalListener();
      participant.removeSignalListener = null;
    }
  }
}
