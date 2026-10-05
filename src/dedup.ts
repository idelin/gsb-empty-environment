import type { Clock } from './clock.ts';
import { SystemClock } from './clock.ts';

export interface RequestIdentity {
  clientId: string;
  generation: number;
  requestId: string;
}

export type IndeterminateReason = 'expired-record' | 'retired-generation';

export type ExecuteResult<T> =
  | { kind: 'fresh'; value: T }
  | { kind: 'joined'; value: T }
  | { kind: 'replayed'; value: T }
  | { kind: 'indeterminate'; reason: IndeterminateReason };

interface RecordEntry {
  clientId: string;
  generation: number;
  state: 'in-flight' | 'settled';
  ok: boolean;
  value: unknown;
  error: unknown;
  settledAt: number;
  expiresAt: number;
  promise: Promise<unknown> | null;
}

export interface DeduplicatorOptions {
  retentionMs: number;
  clock?: Clock;
}

export class RequestDeduplicator {
  private readonly retentionMs: number;
  private readonly clock: Clock;
  private readonly records = new Map<string, RecordEntry>();
  private readonly retiredGenerations = new Map<string, number>();

  constructor(options: DeduplicatorOptions) {
    if (!Number.isFinite(options.retentionMs) || options.retentionMs <= 0) {
      throw new RangeError('retentionMs must be a positive finite number');
    }
    this.retentionMs = options.retentionMs;
    this.clock = options.clock ?? new SystemClock();
  }

  get size(): number {
    return this.records.size;
  }

  retiredGeneration(clientId: string): number {
    return this.retiredGenerations.get(clientId) ?? -1;
  }

  async execute<T>(id: RequestIdentity, fn: () => Promise<T>): Promise<ExecuteResult<T>> {
    validateIdentity(id);
    const key = keyOf(id);
    const now = this.clock.now();

    const existing = this.records.get(key);
    if (existing !== undefined) {
      if (existing.state === 'in-flight') {
        const value = (await existing.promise) as T;
        return { kind: 'joined', value };
      }
      if (now < existing.expiresAt) {
        return replay(existing) as ExecuteResult<T>;
      }
      this.evict(key, existing);
      return { kind: 'indeterminate', reason: 'expired-record' };
    }

    if (id.generation <= this.retiredGeneration(id.clientId)) {
      return { kind: 'indeterminate', reason: 'retired-generation' };
    }

    const entry: RecordEntry = {
      clientId: id.clientId,
      generation: id.generation,
      state: 'in-flight',
      ok: false,
      value: undefined,
      error: undefined,
      settledAt: 0,
      expiresAt: Number.POSITIVE_INFINITY,
      promise: null,
    };
    const promise = run(fn);
    entry.promise = promise;
    this.records.set(key, entry);
    promise.then(
      (value) => this.settle(entry, true, value),
      (error) => this.settle(entry, false, undefined, error),
    );

    const value = (await promise) as T;
    return { kind: 'fresh', value };
  }

  cleanup(): number {
    const now = this.clock.now();
    let evicted = 0;
    for (const [key, entry] of this.records) {
      if (entry.state === 'settled' && now >= entry.expiresAt) {
        this.evict(key, entry);
        evicted += 1;
      }
    }
    return evicted;
  }

  private settle(entry: RecordEntry, ok: boolean, value?: unknown, error?: unknown): void {
    entry.state = 'settled';
    entry.ok = ok;
    entry.value = value;
    entry.error = error;
    entry.settledAt = this.clock.now();
    entry.expiresAt = entry.settledAt + this.retentionMs;
    entry.promise = null;
  }

  private evict(key: string, entry: RecordEntry): void {
    this.records.delete(key);
    if (entry.generation > this.retiredGeneration(entry.clientId)) {
      this.retiredGenerations.set(entry.clientId, entry.generation);
    }
  }
}

function run<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return Promise.resolve(fn());
  } catch (error) {
    return Promise.reject(error);
  }
}

function replay(entry: RecordEntry): ExecuteResult<unknown> {
  if (!entry.ok) {
    throw entry.error;
  }
  return { kind: 'replayed', value: entry.value };
}

function keyOf(id: RequestIdentity): string {
  return JSON.stringify([id.clientId, id.generation, id.requestId]);
}

function validateIdentity(id: RequestIdentity): void {
  if (typeof id.clientId !== 'string' || id.clientId.length === 0) {
    throw new TypeError('clientId must be a non-empty string');
  }
  if (typeof id.requestId !== 'string' || id.requestId.length === 0) {
    throw new TypeError('requestId must be a non-empty string');
  }
  if (!Number.isInteger(id.generation) || id.generation < 0) {
    throw new TypeError('generation must be a non-negative integer');
  }
}
