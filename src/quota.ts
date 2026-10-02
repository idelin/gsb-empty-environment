import { QuotaExceededError } from './errors.ts';
import type { RuleSandbox } from './sandbox.ts';
import type { Value } from './evaluator.ts';

export interface QuotaOptions {
  /** Max simultaneous evaluations per tenant (default 4). */
  maxConcurrent?: number;
  /** Max evaluations per tenant per rolling window (default 100). */
  maxEvaluationsPerWindow?: number;
  /** Max total instruction steps per tenant per rolling window (default 100_000). */
  maxStepsPerWindow?: number;
  /** Rolling window length in ms (default 60_000). */
  windowMs?: number;
  /** Injectable clock for testing. */
  now?: () => number;
}

interface TenantLedger {
  active: number;
  /** Timestamps of finished evaluations inside the current window. */
  evaluations: number[];
  /** [timestamp, steps] pairs inside the current window. */
  steps: Array<[number, number]>;
}

/**
 * Per-tenant execution quota guard. Tenants are fully isolated: one tenant
 * exhausting its quota never affects the others.
 */
export class QuotaGuard {
  private readonly maxConcurrent: number;
  private readonly maxEvaluationsPerWindow: number;
  private readonly maxStepsPerWindow: number;
  private readonly windowMs: number;
  private readonly now: () => number;
  private readonly tenants = new Map<string, TenantLedger>();

  constructor(options: QuotaOptions = {}) {
    this.maxConcurrent = options.maxConcurrent ?? 4;
    this.maxEvaluationsPerWindow = options.maxEvaluationsPerWindow ?? 100;
    this.maxStepsPerWindow = options.maxStepsPerWindow ?? 100_000;
    this.windowMs = options.windowMs ?? 60_000;
    this.now = options.now ?? Date.now;
  }

  private ledger(tenantId: string): TenantLedger {
    let ledger = this.tenants.get(tenantId);
    if (!ledger) {
      ledger = { active: 0, evaluations: [], steps: [] };
      this.tenants.set(tenantId, ledger);
    }
    return ledger;
  }

  private prune(ledger: TenantLedger, now: number): void {
    const cutoff = now - this.windowMs;
    ledger.evaluations = ledger.evaluations.filter((t) => t > cutoff);
    ledger.steps = ledger.steps.filter(([t]) => t > cutoff);
  }

  /**
   * Acquire a slot for the tenant. Throws QuotaExceededError when the tenant
   * is over its concurrency, evaluation-count or step budget. Returns a
   * release function that MUST be called (ideally in `finally`) with the
   * number of instruction steps the evaluation consumed.
   */
  acquire(tenantId: string): (stepsUsed?: number) => void {
    const now = this.now();
    const ledger = this.ledger(tenantId);
    this.prune(ledger, now);

    if (ledger.active >= this.maxConcurrent) {
      throw new QuotaExceededError(tenantId, `concurrency limit ${this.maxConcurrent} reached`);
    }
    if (ledger.evaluations.length >= this.maxEvaluationsPerWindow) {
      throw new QuotaExceededError(
        tenantId,
        `evaluation limit ${this.maxEvaluationsPerWindow}/${this.windowMs}ms reached`,
      );
    }
    const stepsInWindow = ledger.steps.reduce((sum, [, s]) => sum + s, 0);
    if (stepsInWindow >= this.maxStepsPerWindow) {
      throw new QuotaExceededError(
        tenantId,
        `step budget ${this.maxStepsPerWindow}/${this.windowMs}ms exhausted`,
      );
    }

    ledger.active++;
    let released = false;
    return (stepsUsed = 0) => {
      if (released) return;
      released = true;
      ledger.active--;
      const doneAt = this.now();
      ledger.evaluations.push(doneAt);
      ledger.steps.push([doneAt, stepsUsed]);
    };
  }

  /**
   * Run a sandboxed expression under the tenant's quota. Step usage is
   * accounted even when the expression fails (e.g. BudgetExceeded), so
   * tenants cannot dodge their quota with abusive expressions.
   */
  execute(tenantId: string, sandbox: RuleSandbox, source: string, variables: Record<string, Value> = {}): Value {
    const release = this.acquire(tenantId);
    let steps = 0;
    try {
      const result = sandbox.run(source, variables);
      steps = result.stepsUsed;
      return result.value;
    } catch (err) {
      const billed = (err as { stepsUsed?: unknown })?.stepsUsed;
      if (typeof billed === 'number') steps = billed;
      throw err;
    } finally {
      release(steps);
    }
  }
}
