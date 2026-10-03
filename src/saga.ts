import type { SagaEvent } from './types.ts';

export type SagaStatus = 'running' | 'compensating' | 'completed' | 'compensated' | 'failed';

export interface SagaStep<C> {
  name: string;
  execute: (ctx: C) => void | Promise<void>;
  compensate: (ctx: C) => void | Promise<void>;
}

export interface SagaRecord {
  id: string;
  status: SagaStatus;
  executedSteps: string[];
  compensatedSteps: string[];
  error?: string;
}

/**
 * Saga state machine, rebuilt purely from committed log events.
 * Every node that applies the same committed prefix derives the same state,
 * so orchestration survives leader failover.
 */
export class SagaStateMachine {
  private readonly sagas = new Map<string, SagaRecord>();

  apply(event: SagaEvent): void {
    let rec = this.sagas.get(event.sagaId);
    if (!rec) {
      rec = { id: event.sagaId, status: 'running', executedSteps: [], compensatedSteps: [] };
      this.sagas.set(event.sagaId, rec);
    }
    switch (event.type) {
      case 'step-executed':
        if (rec.status === 'running' && event.step && !rec.executedSteps.includes(event.step)) {
          rec.executedSteps.push(event.step);
        }
        break;
      case 'step-failed':
        if (rec.status === 'running') {
          rec.status = 'compensating';
          rec.error = event.error;
        }
        break;
      case 'step-compensated':
        if (rec.status === 'compensating' && event.step && !rec.compensatedSteps.includes(event.step)) {
          rec.compensatedSteps.push(event.step);
        }
        break;
      case 'saga-completed':
        if (rec.status === 'running') rec.status = 'completed';
        break;
      case 'saga-compensated':
        if (rec.status === 'compensating') rec.status = 'compensated';
        break;
      case 'saga-failed':
        if (rec.status === 'compensating') {
          rec.status = 'failed';
          rec.error = event.error ?? rec.error;
        }
        break;
    }
  }

  get(id: string): SagaRecord | undefined {
    const rec = this.sagas.get(id);
    return rec
      ? { ...rec, executedSteps: [...rec.executedSteps], compensatedSteps: [...rec.compensatedSteps] }
      : undefined;
  }

  isStepExecuted(id: string, step: string): boolean {
    return this.sagas.get(id)?.executedSteps.includes(step) ?? false;
  }

  isStepCompensated(id: string, step: string): boolean {
    return this.sagas.get(id)?.compensatedSteps.includes(step) ?? false;
  }
}

/**
 * Resource-side idempotency journal. Side effects registered through `once`
 * are applied at most once per key, even across retries and leader failover.
 */
export class Effects {
  private readonly done = new Set<string>();

  async once(key: string, fn: () => void | Promise<void>): Promise<void> {
    if (this.done.has(key)) return;
    await fn();
    this.done.add(key);
  }

  has(key: string): boolean {
    return this.done.has(key);
  }

  keys(): string[] {
    return [...this.done].sort();
  }
}

export interface OrchestratorOptions {
  /** Compensation attempts per step before the saga is marked failed. */
  maxCompensateRetries?: number;
}

/**
 * Reverse-compensation orchestrator. Every state transition is proposed to
 * the replicated log and only takes effect once committed, giving durable,
 * exactly-once step tracking. Side effects are guarded by idempotency keys.
 */
export class SagaOrchestrator<C> {
  private readonly proposeAndWait: (event: SagaEvent) => Promise<void>;
  private readonly state: SagaStateMachine;
  private readonly maxCompensateRetries: number;
  private readonly inflight = new Map<string, Promise<SagaRecord>>();

  constructor(
    proposeAndWait: (event: SagaEvent) => Promise<void>,
    state: SagaStateMachine,
    opts: OrchestratorOptions = {},
  ) {
    this.proposeAndWait = proposeAndWait;
    this.state = state;
    this.maxCompensateRetries = opts.maxCompensateRetries ?? 3;
  }

  /** Idempotent submission: the same sagaId always resolves to one execution. */
  submit(id: string, steps: SagaStep<C>[], ctx: C): Promise<SagaRecord> {
    const existing = this.inflight.get(id);
    if (existing) return existing;
    const committed = this.state.get(id);
    if (
      committed &&
      (committed.status === 'completed' ||
        committed.status === 'compensated' ||
        committed.status === 'failed')
    ) {
      return Promise.resolve(committed);
    }
    const promise = this.run(id, steps, ctx);
    this.inflight.set(id, promise);
    return promise;
  }

  private async run(id: string, steps: SagaStep<C>[], ctx: C): Promise<SagaRecord> {
    const executed: SagaStep<C>[] = [];
    for (const step of steps) {
      // Recovery: steps already committed as executed are never re-run.
      if (this.state.isStepExecuted(id, step.name)) {
        executed.push(step);
        continue;
      }
      try {
        await step.execute(ctx);
      } catch (err) {
        const error = err instanceof Error ? err.message : String(err);
        await this.proposeAndWait({ sagaId: id, type: 'step-failed', step: step.name, error });
        return this.compensate(id, executed, ctx, error);
      }
      await this.proposeAndWait({ sagaId: id, type: 'step-executed', step: step.name });
      executed.push(step);
    }
    await this.proposeAndWait({ sagaId: id, type: 'saga-completed' });
    return this.record(id, 'completed', steps.map((s) => s.name), []);
  }

  /** Compensate executed steps in exact reverse order, with bounded retries. */
  private async compensate(
    id: string,
    executed: SagaStep<C>[],
    ctx: C,
    cause: string,
  ): Promise<SagaRecord> {
    const compensated: string[] = [];
    for (const step of [...executed].reverse()) {
      if (this.state.isStepCompensated(id, step.name)) {
        compensated.push(step.name);
        continue;
      }
      let ok = false;
      let lastError = cause;
      for (let attempt = 0; attempt < this.maxCompensateRetries && !ok; attempt++) {
        try {
          await step.compensate(ctx);
          ok = true;
        } catch (err) {
          lastError = err instanceof Error ? err.message : String(err);
        }
      }
      if (!ok) {
        await this.proposeAndWait({ sagaId: id, type: 'saga-failed', error: lastError });
        return this.record(id, 'failed', executed.map((s) => s.name), compensated, lastError);
      }
      await this.proposeAndWait({ sagaId: id, type: 'step-compensated', step: step.name });
      compensated.push(step.name);
    }
    await this.proposeAndWait({ sagaId: id, type: 'saga-compensated' });
    return this.record(id, 'compensated', executed.map((s) => s.name), compensated, cause);
  }

  private record(
    id: string,
    status: SagaStatus,
    executedSteps: string[],
    compensatedSteps: string[],
    error?: string,
  ): SagaRecord {
    return { id, status, executedSteps, compensatedSteps, error };
  }
}
