import { PaxosNode, NoMajorityError, NotCommittedError } from './paxos.ts';
import type { LogEntry } from './paxos.ts';

/**
 * Reverse-compensation (saga) transaction orchestrator.
 *
 * Every state transition is committed through the replicated Paxos log, so
 * orchestration survives leader failover: a new orchestrator replays the
 * committed event stream and resumes in-flight sagas (forward recovery for
 * running sagas, reverse compensation for failed ones) until every saga
 * reaches a terminal state — final consistency.
 *
 * Side effects are routed through an IdempotentEffects journal keyed by
 * deterministic idempotency keys, making re-execution after a crash safe.
 */

export type SagaEvent =
  | { type: 'saga-started'; sagaId: string; steps: string[] }
  | { type: 'step-executed'; sagaId: string; step: string; key: string }
  | { type: 'step-failed'; sagaId: string; step: string; error: string }
  | { type: 'step-compensated'; sagaId: string; step: string; key: string }
  | { type: 'saga-completed'; sagaId: string }
  | { type: 'saga-compensated'; sagaId: string };

export type SagaStatus = 'running' | 'compensating' | 'completed' | 'compensated';

export interface SagaContext {
  sagaId: string;
  data: Record<string, unknown>;
}

export interface StepDef {
  name: string;
  execute: (ctx: SagaContext) => unknown | Promise<unknown>;
  compensate: (ctx: SagaContext) => unknown | Promise<unknown>;
}

/** Deduplicates side effects by idempotency key; tracks in-flight calls. */
export class IdempotentEffects {
  private applied = new Set<string>();
  private inFlight = 0;

  async apply(key: string, fn: () => unknown | Promise<unknown>): Promise<void> {
    if (this.applied.has(key)) return;
    this.inFlight++;
    try {
      await fn();
    } finally {
      this.inFlight--;
    }
    this.applied.add(key);
  }

  has(key: string): boolean {
    return this.applied.has(key);
  }

  get size(): number {
    return this.applied.size;
  }

  /** Leak check: no effect call may be in flight when a test settles. */
  get pending(): number {
    return this.inFlight;
  }
}

interface Projection {
  steps: string[];
  executed: string[]; // in execution order
  compensated: Set<string>;
  failedStep: string | null;
  status: SagaStatus;
}

export class SagaUnavailableError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = 'SagaUnavailableError';
  }
}

export interface OrchestratorOptions {
  /** Max attempts per compensation step before pausing for recovery. */
  compensateRetries?: number;
}

export class SagaOrchestrator {
  private readonly node: PaxosNode;
  readonly effects: IdempotentEffects;
  private readonly compensateRetries: number;

  private projections = new Map<string, Projection>();
  private stepDefs = new Map<string, StepDef[]>();
  private contexts = new Map<string, Record<string, unknown>>();
  private chains = new Map<string, Promise<void>>();
  private activeRuns = 0;

  constructor(node: PaxosNode, effects = new IdempotentEffects(), opts: OrchestratorOptions = {}) {
    this.node = node;
    this.effects = effects;
    this.compensateRetries = opts.compensateRetries ?? 3;
    // Rebuild projections from everything this node has already applied
    // (crash / failover recovery), then follow future commits.
    for (const entry of node.applied) this.project(entry);
    node.onApply = (entry) => this.project(entry);
  }

  // ------------------------------------------------------------ projection

  private project(entry: LogEntry): void {
    if (entry.kind !== 'app') return;
    const event = entry.payload as SagaEvent;
    if (!event || typeof event !== 'object' || !('sagaId' in event)) return;
    let proj = this.projections.get(event.sagaId);
    if (!proj) {
      proj = { steps: [], executed: [], compensated: new Set(), failedStep: null, status: 'running' };
      this.projections.set(event.sagaId, proj);
    }
    switch (event.type) {
      case 'saga-started':
        proj.steps = event.steps;
        break;
      case 'step-executed':
        if (!proj.executed.includes(event.step)) proj.executed.push(event.step);
        break;
      case 'step-failed':
        proj.failedStep = event.step;
        proj.status = 'compensating';
        break;
      case 'step-compensated':
        proj.compensated.add(event.step);
        break;
      case 'saga-completed':
        proj.status = 'completed';
        break;
      case 'saga-compensated':
        proj.status = 'compensated';
        break;
    }
  }

  statusOf(sagaId: string): SagaStatus | 'unknown' {
    return this.projections.get(sagaId)?.status ?? 'unknown';
  }

  projectionOf(sagaId: string): Readonly<Projection> | undefined {
    return this.projections.get(sagaId);
  }

  /** Leak check: no saga run active and every known saga is terminal. */
  get isQuiescent(): boolean {
    if (this.activeRuns !== 0) return false;
    for (const proj of this.projections.values()) {
      if (proj.status === 'running' || proj.status === 'compensating') return false;
    }
    return this.effects.pending === 0;
  }

  get pendingSagaCount(): number {
    let n = 0;
    for (const proj of this.projections.values()) {
      if (proj.status === 'running' || proj.status === 'compensating') n++;
    }
    return n;
  }

  // ------------------------------------------------------------ execution

  /**
   * Run (or resume) a saga. Concurrent calls for the same sagaId are
   * serialized; the saga body itself is idempotent so a retry after a
   * crash simply continues where the replicated log left off.
   */
  runSaga(sagaId: string, steps: StepDef[]): Promise<SagaStatus> {
    this.stepDefs.set(sagaId, steps);
    const prev = this.chains.get(sagaId) ?? Promise.resolve();
    const run = prev.then(() => this.executeSaga(sagaId, steps));
    this.chains.set(
      sagaId,
      run.then(
        () => undefined,
        () => undefined,
      ),
    );
    return run;
  }

  /**
   * Resume every non-terminal saga known from the replicated log.
   * Step definitions for sagas not yet registered on this orchestrator
   * can be supplied (required after a failover to a fresh orchestrator).
   */
  async recover(defs?: ReadonlyMap<string, StepDef[]>): Promise<void> {
    if (defs) {
      for (const [sagaId, steps] of defs) this.stepDefs.set(sagaId, steps);
    }
    const pending: Promise<unknown>[] = [];
    for (const [sagaId, proj] of this.projections) {
      if (proj.status === 'completed' || proj.status === 'compensated') continue;
      const steps = this.stepDefs.get(sagaId);
      if (!steps) throw new Error(`cannot recover saga ${sagaId}: step definitions not registered`);
      pending.push(this.runSaga(sagaId, steps));
    }
    await Promise.all(pending);
  }

  private commit(event: SagaEvent): void {
    try {
      this.node.propose({ kind: 'app', payload: event });
    } catch (err) {
      if (err instanceof NoMajorityError || err instanceof NotCommittedError) {
        throw new SagaUnavailableError(`cannot commit ${event.type} for ${event.sagaId}: ${err.message}`);
      }
      throw err;
    }
  }

  private ctx(sagaId: string): SagaContext {
    let data = this.contexts.get(sagaId);
    if (!data) {
      data = {};
      this.contexts.set(sagaId, data);
    }
    return { sagaId, data };
  }

  private async executeSaga(sagaId: string, steps: StepDef[]): Promise<SagaStatus> {
    this.activeRuns++;
    try {
      let proj = this.projections.get(sagaId);
      if (!proj) {
        this.commit({ type: 'saga-started', sagaId, steps: steps.map((s) => s.name) });
        proj = this.projections.get(sagaId)!;
      }
      if (proj.status === 'completed' || proj.status === 'compensated') return proj.status;

      if (proj.status === 'compensating') {
        await this.compensateAll(sagaId, steps, proj);
        return this.projections.get(sagaId)!.status;
      }

      for (const step of steps) {
        if (proj.executed.includes(step.name)) continue; // already done (recovery)
        try {
          await this.effects.apply(this.key(sagaId, step.name, 'execute'), () =>
            step.execute(this.ctx(sagaId)),
          );
        } catch (err) {
          this.commit({
            type: 'step-failed',
            sagaId,
            step: step.name,
            error: err instanceof Error ? err.message : String(err),
          });
          await this.compensateAll(sagaId, steps, this.projections.get(sagaId)!);
          return this.projections.get(sagaId)!.status;
        }
        this.commit({ type: 'step-executed', sagaId, step: step.name, key: this.key(sagaId, step.name, 'execute') });
        proj = this.projections.get(sagaId)!;
      }
      this.commit({ type: 'saga-completed', sagaId });
      return 'completed';
    } finally {
      this.activeRuns--;
    }
  }

  /** Compensate executed-but-not-compensated steps in strict reverse order. */
  private async compensateAll(sagaId: string, steps: StepDef[], proj: Projection): Promise<void> {
    const remaining = proj.executed.filter((name) => !proj.compensated.has(name)).reverse();
    for (const name of remaining) {
      const step = steps.find((s) => s.name === name);
      if (!step) throw new Error(`missing step definition for ${name}`);
      let attempts = 0;
      for (;;) {
        try {
          await this.effects.apply(this.key(sagaId, name, 'compensate'), () =>
            step.compensate(this.ctx(sagaId)),
          );
          break;
        } catch (err) {
          attempts++;
          if (attempts >= this.compensateRetries) {
            // Pause: saga stays in 'compensating'; a later recover() resumes it.
            return;
          }
        }
      }
      this.commit({ type: 'step-compensated', sagaId, step: name, key: this.key(sagaId, name, 'compensate') });
    }
    this.commit({ type: 'saga-compensated', sagaId });
  }

  private key(sagaId: string, step: string, phase: 'execute' | 'compensate'): string {
    return `${sagaId}:${step}:${phase}`;
  }
}
