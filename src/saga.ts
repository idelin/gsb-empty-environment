/**
 * Saga orchestrator: reverse compensation & transactional rollback
 * state orchestration.
 *
 * Semantics:
 * - Steps execute sequentially. On any step failure, already-executed
 *   steps are compensated in strict reverse order (LIFO).
 * - Idempotency: a transaction id is executed at most once. Concurrent
 *   or repeated run() calls with the same id share a single execution.
 *   Each executed step is compensated at most once, even across retries.
 * - Exactly-once compensation effect: compensation is retried on failure
 *   (configurable), and remaining steps are still compensated even if one
 *   compensation permanently fails, so no other resource leaks.
 * - Leak detection: assertNoLeaks() verifies every transaction reached a
 *   terminal state and no executed step was left uncompensated.
 */

export type TxState =
  | 'PENDING'
  | 'RUNNING'
  | 'COMPLETED'
  | 'COMPENSATING'
  | 'COMPENSATED'
  | 'COMPENSATION_FAILED';

export type StepStatus =
  | 'EXECUTED'
  | 'COMPENSATING'
  | 'COMPENSATED'
  | 'COMPENSATION_FAILED';

export interface StepJournal<C = unknown> {
  readonly name: string;
  readonly idempotencyKey: string;
  status: StepStatus;
  /** Opaque data returned by execute(), handed back to compensate(). */
  readonly data: C;
  executeAttempts: number;
  compensateAttempts: number;
}

export interface StateTransition {
  readonly from: TxState;
  readonly to: TxState;
  readonly at: number;
}

export interface TransactionSnapshot {
  readonly id: string;
  readonly state: TxState;
  readonly steps: readonly StepJournal[];
  readonly error?: string;
  readonly transitions: readonly StateTransition[];
}

export interface StepHandle {
  readonly txId: string;
  readonly stepName: string;
  readonly idempotencyKey: string;
  readonly attempt: number;
}

export interface SagaStep<TCtx, C = unknown> {
  readonly name: string;
  execute(ctx: TCtx, handle: StepHandle): C | Promise<C>;
  compensate(ctx: TCtx, data: C, handle: StepHandle): void | Promise<void>;
}

export class SagaFailure extends Error {
  readonly txId: string;
  readonly stepName: string;
  readonly snapshot: TransactionSnapshot;
  constructor(txId: string, stepName: string, cause: unknown, snapshot: TransactionSnapshot) {
    super(
      `transaction ${txId} failed at step "${stepName}": ` +
      (cause instanceof Error ? cause.message : String(cause)),
    );
    this.name = 'SagaFailure';
    this.txId = txId;
    this.stepName = stepName;
    this.cause = cause;
    this.snapshot = snapshot;
  }
}

interface TransactionRecord {
  id: string;
  state: TxState;
  journal: StepJournal[];
  transitions: StateTransition[];
  error?: string;
  promise?: Promise<TransactionSnapshot>;
}

export interface SagaOptions {
  /** Compensation attempts per step before giving up. Default 3. */
  readonly compensationRetries?: number;
  /** Backoff between compensation retries. Default: immediate. */
  readonly backoffMs?: (attempt: number) => number;
  readonly now?: () => number;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class SagaOrchestrator<TCtx = unknown> {
  private readonly txs = new Map<string, TransactionRecord>();
  private readonly compensationRetries: number;
  private readonly backoffMs: (attempt: number) => number;
  private readonly now: () => number;

  constructor(options: SagaOptions = {}) {
    this.compensationRetries = Math.max(1, options.compensationRetries ?? 3);
    this.backoffMs = options.backoffMs ?? (() => 0);
    this.now = options.now ?? (() => Date.now());
  }

  /**
   * Runs a transaction. Idempotent by `id`: the first call executes,
   * subsequent calls with the same id return the same outcome without
   * re-executing any step.
   */
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  run(id: string, steps: readonly SagaStep<TCtx, any>[], ctx: TCtx): Promise<TransactionSnapshot> {
    // Synchronous check-then-set: atomic under Node's single-threaded
    // event loop, so concurrent run() calls can never double-execute.
    const existing = this.txs.get(id);
    if (existing?.promise !== undefined) return existing.promise;

    const record: TransactionRecord = {
      id,
      state: 'PENDING',
      journal: [],
      transitions: [],
    };
    record.promise = this.execute(record, steps, ctx);
    this.txs.set(id, record);
    return record.promise;
  }

  snapshot(id: string): TransactionSnapshot | undefined {
    const record = this.txs.get(id);
    return record === undefined ? undefined : this.toSnapshot(record);
  }

  listTransactions(): TransactionSnapshot[] {
    return [...this.txs.values()].map((record) => this.toSnapshot(record));
  }

  /**
   * Strict leak assertion: every transaction must be in a terminal state,
   * and no executed step may be left uncompensated in a rolled-back
   * transaction. Throws describing every leak found.
   */
  assertNoLeaks(): void {
    const problems: string[] = [];
    for (const record of this.txs.values()) {
      const terminal =
        record.state === 'COMPLETED' ||
        record.state === 'COMPENSATED' ||
        record.state === 'COMPENSATION_FAILED';
      if (!terminal) {
        problems.push(`tx ${record.id}: non-terminal state ${record.state}`);
      }
      if (record.state === 'COMPENSATION_FAILED') {
        problems.push(`tx ${record.id}: compensation failed (resource leak)`);
      }
      for (const step of record.journal) {
        if (record.state !== 'COMPLETED' &&
            (step.status === 'EXECUTED' || step.status === 'COMPENSATING')) {
          problems.push(
            `tx ${record.id}: step "${step.name}" executed but not compensated (${step.status})`,
          );
        }
        if (step.status === 'COMPENSATION_FAILED') {
          problems.push(`tx ${record.id}: step "${step.name}" compensation failed`);
        }
      }
    }
    if (problems.length > 0) {
      throw new Error(`saga leak detected:\n  ${problems.join('\n  ')}`);
    }
  }

  private transition(record: TransactionRecord, to: TxState): void {
    record.transitions.push({ from: record.state, to, at: this.now() });
    record.state = to;
  }

  private toSnapshot(record: TransactionRecord): TransactionSnapshot {
    return {
      id: record.id,
      state: record.state,
      steps: record.journal.map((step) => ({ ...step })),
      error: record.error,
      transitions: [...record.transitions],
    };
  }

  private async execute(
    record: TransactionRecord,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    steps: readonly SagaStep<TCtx, any>[],
    ctx: TCtx,
  ): Promise<TransactionSnapshot> {
    this.transition(record, 'RUNNING');
    for (let i = 0; i < steps.length; i++) {
      const step = steps[i]!;
      const key = `${record.id}#${i}:${step.name}`;
      const handle: StepHandle = {
        txId: record.id, stepName: step.name, idempotencyKey: key, attempt: 1,
      };
      let data: unknown;
      try {
        data = await step.execute(ctx, handle);
      } catch (cause) {
        record.error = cause instanceof Error ? cause.message : String(cause);
        await this.compensate(record, steps, ctx, i - 1);
        throw new SagaFailure(record.id, step.name, cause, this.toSnapshot(record));
      }
      record.journal.push({
        name: step.name,
        idempotencyKey: key,
        status: 'EXECUTED',
        data,
        executeAttempts: 1,
        compensateAttempts: 0,
      });
    }
    this.transition(record, 'COMPLETED');
    return this.toSnapshot(record);
  }

  /** Compensates journal entries [0..fromIndex] in strict reverse order. */
  private async compensate(
    record: TransactionRecord,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    steps: readonly SagaStep<TCtx, any>[],
    ctx: TCtx,
    fromIndex: number,
  ): Promise<void> {
    this.transition(record, 'COMPENSATING');
    let allCompensated = true;
    for (let j = fromIndex; j >= 0; j--) {
      const entry = record.journal[j];
      if (entry === undefined || entry.status !== 'EXECUTED') {
        // Already compensated (or never executed): skip. This is the
        // exactly-once guard — a step is never compensated twice.
        continue;
      }
      entry.status = 'COMPENSATING';
      let ok = false;
      for (let attempt = 1; attempt <= this.compensationRetries; attempt++) {
        entry.compensateAttempts = attempt;
        const handle: StepHandle = {
          txId: record.id,
          stepName: entry.name,
          idempotencyKey: entry.idempotencyKey,
          attempt,
        };
        try {
          await steps[j]!.compensate(ctx, entry.data, handle);
          ok = true;
          break;
        } catch {
          if (attempt < this.compensationRetries) {
            const delay = this.backoffMs(attempt);
            if (delay > 0) await sleep(delay);
          }
        }
      }
      entry.status = ok ? 'COMPENSATED' : 'COMPENSATION_FAILED';
      if (!ok) allCompensated = false;
      // Continue compensating the remaining steps regardless: one bad
      // compensation must not leak the other steps' resources.
    }
    this.transition(record, allCompensated ? 'COMPENSATED' : 'COMPENSATION_FAILED');
  }
}
