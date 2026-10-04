import { systemClock, type Clock } from './clock.ts';

export type Generation = number;

export type Outcome<T> =
  | { status: 'ok'; value: T }
  | { status: 'error'; error: unknown }
  | { status: 'undetermined' };

export const UNDETERMINED: Outcome<never> = Object.freeze({
  status: 'undetermined',
} as const);

export function isUndetermined<T>(outcome: Outcome<T>): boolean {
  return outcome.status === 'undetermined';
}

interface RecordEntry {
  readonly generation: Generation;
  state: 'pending' | 'completed';
  expiresAt: number;
  shared: Promise<Outcome<unknown>> | null;
  outcome: Outcome<unknown> | null;
}

export interface DedupOptions {
  pendingTtlMs: number;
  resultTtlMs: number;
  clock?: Clock;
}

export class RequestDeduplicator {
  private readonly records = new Map<string, RecordEntry>();
  private readonly pendingTtlMs: number;
  private readonly resultTtlMs: number;
  private readonly clock: Clock;
  private nextGeneration = 1;

  constructor(options: DedupOptions) {
    if (!(options.pendingTtlMs > 0)) {
      throw new RangeError('pendingTtlMs must be positive');
    }
    if (!(options.resultTtlMs > 0)) {
      throw new RangeError('resultTtlMs must be positive');
    }
    this.pendingTtlMs = options.pendingTtlMs;
    this.resultTtlMs = options.resultTtlMs;
    this.clock = options.clock ?? systemClock;
  }

  begin(key: string): Generation {
    this.evictIfExpired(key);
    const existing = this.records.get(key);
    if (existing) {
      return existing.generation;
    }
    const generation = this.nextGeneration++;
    this.records.set(key, {
      generation,
      state: 'pending',
      expiresAt: this.clock.now() + this.pendingTtlMs,
      shared: null,
      outcome: null,
    });
    return generation;
  }

  execute<T>(
    key: string,
    generation: Generation,
    fn: () => Promise<T> | T,
  ): Promise<Outcome<T>> {
    this.evictIfExpired(key);
    const record = this.records.get(key);
    if (!record || record.generation !== generation) {
      return Promise.resolve(UNDETERMINED);
    }
    if (record.state === 'completed') {
      return Promise.resolve(record.outcome as Outcome<T>);
    }
    if (!record.shared) {
      record.shared = Promise.resolve()
        .then(fn)
        .then(
          (value) => this.complete(key, record, { status: 'ok', value }),
          (error) => this.complete(key, record, { status: 'error', error }),
        );
    }
    return record.shared as Promise<Outcome<T>>;
  }

  sweep(now: number = this.clock.now()): number {
    let removed = 0;
    for (const [key, record] of this.records) {
      if (now >= record.expiresAt) {
        this.records.delete(key);
        removed += 1;
      }
    }
    return removed;
  }

  has(key: string): boolean {
    this.evictIfExpired(key);
    return this.records.has(key);
  }

  get size(): number {
    this.sweep();
    return this.records.size;
  }

  private complete(
    key: string,
    record: RecordEntry,
    outcome: Outcome<unknown>,
  ): Outcome<unknown> {
    if (this.records.get(key) === record) {
      record.state = 'completed';
      record.outcome = outcome;
      record.expiresAt = this.clock.now() + this.resultTtlMs;
    }
    return outcome;
  }

  private evictIfExpired(key: string): void {
    const record = this.records.get(key);
    if (record && this.clock.now() >= record.expiresAt) {
      this.records.delete(key);
    }
  }
}
