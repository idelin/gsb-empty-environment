import { evaluate, type Attributes, type Policy } from './policy.ts';

export interface ObligationContext {
  readonly executionId: string;
  readonly attributes: Attributes;
  idempotencyKeyFor(obligationId: string, phase: 'execute' | 'compensate'): string;
}

export interface Obligation {
  readonly id: string;
  readonly kind: 'rollbackable' | 'non-rollbackable';
  readonly dependsOn?: readonly string[];
  execute(ctx: ObligationContext): Promise<void> | void;
  compensate?(ctx: ObligationContext): Promise<void> | void;
}

export type FinalStatus =
  | 'denied'
  | 'committed'
  | 'compensated'
  | 'compensation_pending'
  | 'commit_failed';

export interface ExecutionResult {
  readonly executionId: string;
  readonly status: FinalStatus;
  readonly executed: readonly string[];
  readonly compensated: readonly string[];
  readonly pendingCompensations: readonly string[];
  readonly failedObligation?: string;
  readonly error?: string;
}

export interface ExecutorOptions {
  readonly maxCompensationAttempts?: number;
  readonly retryDelayMs?: number;
  readonly now?: () => number;
}

interface ExecutionRecord {
  executed: string[];
  compensated: Set<string>;
  pending: Set<string>;
  status: FinalStatus;
}

export class ObligationError extends Error {}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export class ObligationExecutor {
  private readonly journal = new Map<string, ExecutionRecord>();
  private readonly maxAttempts: number;
  private readonly retryDelayMs: number;

  constructor(options: ExecutorOptions = {}) {
    this.maxAttempts = options.maxCompensationAttempts ?? 3;
    this.retryDelayMs = options.retryDelayMs ?? 0;
  }

  async authorizeAndRun(
    policy: Policy,
    attributes: Attributes,
    obligations: readonly Obligation[],
    executionId: string,
  ): Promise<ExecutionResult> {
    if (!evaluate(policy, attributes)) {
      return this.result(executionId, 'denied');
    }

    const { rollbackable, nonRollbackable } = this.validateAndOrder(obligations);
    const record: ExecutionRecord = {
      executed: [],
      compensated: new Set(),
      pending: new Set(),
      status: 'committed',
    };
    this.journal.set(executionId, record);
    const ctx = this.makeContext(executionId, attributes);

    for (const obligation of rollbackable) {
      try {
        await obligation.execute(ctx);
        record.executed.push(obligation.id);
      } catch (err) {
        record.status = await this.compensateAll(record, rollbackable, ctx);
        return this.toResult(executionId, record, obligation.id, err);
      }
    }

    for (const obligation of nonRollbackable) {
      try {
        await obligation.execute(ctx);
      } catch (err) {
        record.status = 'commit_failed';
        return this.toResult(executionId, record, obligation.id, err);
      }
    }

    record.status = 'committed';
    return this.toResult(executionId, record);
  }

  async resumePendingCompensations(
    executionId: string,
    obligations: readonly Obligation[],
    attributes: Attributes = {},
  ): Promise<ExecutionResult> {
    const record = this.journal.get(executionId);
    if (!record) throw new ObligationError(`unknown execution "${executionId}"`);
    if (record.status !== 'compensation_pending') {
      return this.toResult(executionId, record);
    }
    const ctx = this.makeContext(executionId, attributes);
    record.status = await this.compensateAll(record, obligations, ctx);
    return this.toResult(executionId, record);
  }

  private async compensateAll(
    record: ExecutionRecord,
    obligations: readonly Obligation[],
    ctx: ObligationContext,
  ): Promise<FinalStatus> {
    const byId = new Map(obligations.map((o) => [o.id, o]));
    const toCompensate = record.executed
      .filter((id) => !record.compensated.has(id))
      .reverse();

    for (const id of toCompensate) {
      const obligation = byId.get(id);
      if (!obligation?.compensate) {
        record.pending.add(id);
        continue;
      }
      let done = false;
      for (let attempt = 1; attempt <= this.maxAttempts && !done; attempt++) {
        try {
          await obligation.compensate(ctx);
          done = true;
        } catch {
          if (attempt < this.maxAttempts && this.retryDelayMs > 0) {
            await sleep(this.retryDelayMs);
          }
        }
      }
      if (done) {
        record.compensated.add(id);
        record.pending.delete(id);
      } else {
        record.pending.add(id);
      }
    }
    return record.pending.size === 0 ? 'compensated' : 'compensation_pending';
  }

  private validateAndOrder(obligations: readonly Obligation[]): {
    rollbackable: Obligation[];
    nonRollbackable: Obligation[];
  } {
    const byId = new Map<string, Obligation>();
    for (const o of obligations) {
      if (byId.has(o.id)) throw new ObligationError(`duplicate obligation id "${o.id}"`);
      byId.set(o.id, o);
    }
    const rollbackable: Obligation[] = [];
    const nonRollbackable: Obligation[] = [];
    for (const o of obligations) {
      if (o.kind === 'rollbackable') {
        if (typeof o.compensate !== 'function') {
          throw new ObligationError(`rollbackable obligation "${o.id}" must define compensate()`);
        }
        rollbackable.push(o);
      } else if (o.kind === 'non-rollbackable') {
        nonRollbackable.push(o);
      } else {
        throw new ObligationError(`obligation "${o.id}" has unknown kind "${o.kind}"`);
      }
    }
    for (const o of rollbackable) {
      for (const dep of o.dependsOn ?? []) {
        const target = byId.get(dep);
        if (!target) throw new ObligationError(`obligation "${o.id}" depends on unknown "${dep}"`);
        if (target.kind !== 'rollbackable') {
          throw new ObligationError(
            `rollbackable obligation "${o.id}" cannot depend on non-rollbackable "${dep}"`,
          );
        }
      }
    }
    return { rollbackable: topoSort(rollbackable), nonRollbackable };
  }

  private makeContext(executionId: string, attributes: Attributes): ObligationContext {
    return {
      executionId,
      attributes,
      idempotencyKeyFor: (obligationId, phase) =>
        `${executionId}:${obligationId}:${phase}`,
    };
  }

  private result(executionId: string, status: FinalStatus): ExecutionResult {
    return {
      executionId,
      status,
      executed: [],
      compensated: [],
      pendingCompensations: [],
    };
  }

  private toResult(
    executionId: string,
    record: ExecutionRecord,
    failedObligation?: string,
    error?: unknown,
  ): ExecutionResult {
    return {
      executionId,
      status: record.status,
      executed: [...record.executed],
      compensated: [...record.compensated],
      pendingCompensations: [...record.pending],
      ...(failedObligation !== undefined ? { failedObligation } : {}),
      ...(error !== undefined
        ? { error: error instanceof Error ? error.message : String(error) }
        : {}),
    };
  }
}

function topoSort(obligations: readonly Obligation[]): Obligation[] {
  const byId = new Map(obligations.map((o) => [o.id, o]));
  const state = new Map<string, 'visiting' | 'done'>();
  const ordered: Obligation[] = [];
  const visit = (o: Obligation) => {
    const s = state.get(o.id);
    if (s === 'done') return;
    if (s === 'visiting') {
      throw new ObligationError(`dependency cycle involving "${o.id}"`);
    }
    state.set(o.id, 'visiting');
    for (const dep of o.dependsOn ?? []) {
      const target = byId.get(dep);
      if (target) visit(target);
    }
    state.set(o.id, 'done');
    ordered.push(o);
  };
  for (const o of obligations) visit(o);
  return ordered;
}
