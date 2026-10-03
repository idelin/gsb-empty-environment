/**
 * Saga-style orchestration for long-lived flows: forward steps with reverse
 * (LIFO) compensation on failure, idempotent execution, retrying compensators
 * for eventual consistency, and concurrency-safe run de-duplication.
 */

export type SagaStatus =
  | 'idle'
  | 'running'
  | 'succeeded'
  | 'compensating'
  | 'rolledback'
  | 'failed';

export interface SagaStep<C> {
  readonly name: string;
  execute(ctx: C): void | Promise<void>;
  compensate(ctx: C): void | Promise<void>;
}

export interface SagaEvent {
  readonly type:
    | 'step-executed'
    | 'step-compensated'
    | 'compensation-retry'
    | 'compensation-failed'
    | 'succeeded'
    | 'rolledback'
    | 'failed';
  readonly step?: string;
  readonly attempt?: number;
  readonly error?: unknown;
}

export interface CompensationFailure {
  readonly step: string;
  error: unknown;
  attempts: number;
}

export interface SagaOptions {
  /** Retries per compensation before it is parked for later recovery. Default 3. */
  readonly maxCompensationRetries?: number;
  /** Delay between compensation retries (ms). Default 10. */
  readonly retryDelayMs?: number;
  /** Injectable sleep (tests can pass a no-op). */
  readonly sleep?: (ms: number) => Promise<void>;
}

export interface RunOptions {
  /** Abort between steps; triggers compensation of everything completed so far. */
  readonly signal?: AbortSignal;
}

export class SagaError extends Error {
  readonly cause: unknown;
  readonly compensationFailures: readonly CompensationFailure[];

  constructor(message: string, cause: unknown, compensationFailures: readonly CompensationFailure[]) {
    super(message);
    this.name = 'SagaError';
    this.cause = cause;
    this.compensationFailures = compensationFailures;
  }
}

const defaultSleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

export class Saga<C> {
  private readonly steps: SagaStep<C>[] = [];
  private completed: SagaStep<C>[] = [];
  private statusValue: SagaStatus = 'idle';
  private inflight: Promise<SagaStatus> | null = null;
  private readonly listeners = new Set<(event: SagaEvent) => void>();
  private readonly failedCompensations: CompensationFailure[] = [];
  private readonly maxRetries: number;
  private readonly retryDelayMs: number;
  private readonly sleep: (ms: number) => Promise<void>;

  constructor(options: SagaOptions = {}) {
    this.maxRetries = options.maxCompensationRetries ?? 3;
    this.retryDelayMs = options.retryDelayMs ?? 10;
    this.sleep = options.sleep ?? defaultSleep;
  }

  get status(): SagaStatus {
    return this.statusValue;
  }

  /** Steps executed but not yet compensated/committed (leak probe). */
  get pendingStepCount(): number {
    return this.completed.length;
  }

  get listenerCount(): number {
    return this.listeners.size;
  }

  get failedCompensationCount(): number {
    return this.failedCompensations.length;
  }

  get compensationFailures(): readonly CompensationFailure[] {
    return this.failedCompensations;
  }

  addStep(step: SagaStep<C>): this {
    if (this.statusValue !== 'idle') {
      throw new Error(`cannot add step "${step.name}" once the saga has started`);
    }
    if (this.steps.some((existing) => existing.name === step.name)) {
      throw new Error(`duplicate step name "${step.name}"`);
    }
    this.steps.push(step);
    return this;
  }

  /** Subscribe to lifecycle events; returns an unsubscribe function. */
  onEvent(listener: (event: SagaEvent) => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private emit(event: SagaEvent): void {
    for (const listener of [...this.listeners]) listener(event);
  }

  /**
   * Run the saga. Concurrent calls share the same in-flight execution, and a
   * call after success returns the cached result, so re-invocation is
   * idempotent: each step executes at most once per attempt.
   */
  run(ctx: C, options: RunOptions = {}): Promise<SagaStatus> {
    if (this.statusValue === 'succeeded') return Promise.resolve('succeeded');
    if (this.inflight) return this.inflight;
    const attempt = this.executeAll(ctx, options).finally(() => {
      this.inflight = null;
    });
    this.inflight = attempt;
    return attempt;
  }

  private async executeAll(ctx: C, options: RunOptions): Promise<SagaStatus> {
    this.statusValue = 'running';
    this.completed = [];
    let failure: unknown = null;
    try {
      for (const step of this.steps) {
        if (options.signal?.aborted) {
          throw options.signal.reason ?? new Error('saga aborted');
        }
        await step.execute(ctx);
        this.completed.push(step);
        this.emit({ type: 'step-executed', step: step.name });
      }
      this.statusValue = 'succeeded';
      this.completed = [];
      this.emit({ type: 'succeeded' });
      return 'succeeded';
    } catch (error) {
      failure = error;
    }

    this.statusValue = 'compensating';
    const toCompensate = this.completed;
    this.completed = [];
    await this.compensateAll(ctx, toCompensate.reverse());

    if (this.failedCompensations.length > 0) {
      this.statusValue = 'failed';
      this.emit({ type: 'failed', error: failure });
      throw new SagaError(
        `saga failed and ${this.failedCompensations.length} compensation(s) could not be completed; ` +
          'call resumeCompensation() to finish rolling back',
        failure,
        [...this.failedCompensations],
      );
    }
    this.statusValue = 'rolledback';
    this.emit({ type: 'rolledback' });
    return 'rolledback';
  }

  private async compensateAll(ctx: C, steps: readonly SagaStep<C>[]): Promise<void> {
    for (const step of steps) {
      await this.compensateStep(ctx, step);
    }
  }

  private async compensateStep(ctx: C, step: SagaStep<C>): Promise<void> {
    let attempt = 0;
    for (;;) {
      try {
        await step.compensate(ctx);
        const parked = this.failedCompensations.findIndex((f) => f.step === step.name);
        if (parked >= 0) this.failedCompensations.splice(parked, 1);
        this.emit({ type: 'step-compensated', step: step.name });
        return;
      } catch (error) {
        attempt += 1;
        if (attempt > this.maxRetries) {
          const existing = this.failedCompensations.find((f) => f.step === step.name);
          if (existing) {
            existing.attempts += attempt;
            existing.error = error;
          } else {
            this.failedCompensations.push({ step: step.name, error, attempts: attempt });
          }
          this.emit({ type: 'compensation-failed', step: step.name, error });
          return;
        }
        this.emit({ type: 'compensation-retry', step: step.name, attempt, error });
        await this.sleep(this.retryDelayMs);
      }
    }
  }

  /**
   * Retry previously parked compensations (eventual consistency). Resolves to
   * 'rolledback' once every failed compensation has been completed.
   */
  async resumeCompensation(ctx: C): Promise<SagaStatus> {
    if (this.statusValue !== 'failed') return this.statusValue;
    const pending = this.failedCompensations
      .map((failure) => this.steps.find((step) => step.name === failure.step))
      .filter((step): step is SagaStep<C> => step !== undefined);
    await this.compensateAll(ctx, pending);
    if (this.failedCompensations.length === 0) {
      this.statusValue = 'rolledback';
      this.emit({ type: 'rolledback' });
    }
    return this.statusValue;
  }

  /** Release all internal references (listeners, pending bookkeeping). */
  dispose(): void {
    this.listeners.clear();
    this.completed = [];
  }
}
