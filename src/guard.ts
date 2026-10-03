import { BudgetExceeded, MemoryLimitExceeded, TimeoutExceeded } from './errors.ts';

export interface Limits {
  /** Maximum number of interpreter steps (one per evaluated AST node). */
  readonly maxSteps: number;
  /** Maximum memory units (1 unit per value allocation + 1 per string char). */
  readonly maxMemory: number;
  /** Wall-clock timeout in milliseconds. */
  readonly timeoutMs: number;
  /** Maximum `eval` re-entry depth (defense-in-depth behind the step budget). */
  readonly maxCallDepth: number;
}

export const DEFAULT_LIMITS: Limits = {
  maxSteps: 1000,
  maxMemory: 100_000,
  timeoutMs: 100,
  maxCallDepth: 4096,
};

/**
 * Execution-limit guard shared by every tenant evaluation. All counters are
 * monotonic for the lifetime of one `evaluate()` call, so recursive `eval`
 * re-entries share the same budget and cannot multiply resources.
 */
export class ResourceGuard {
  private steps = 0;
  private memory = 0;
  private readonly deadline: number;
  private readonly limits: Limits;

  constructor(limits: Limits) {
    this.limits = limits;
    this.deadline = Date.now() + limits.timeoutMs;
  }

  /** Charge one (or more) instruction steps; also polls the wall-clock deadline. */
  step(count = 1): void {
    this.steps += count;
    if (this.steps > this.limits.maxSteps) {
      throw new BudgetExceeded(
        `Step budget exhausted: ${this.steps} > ${this.limits.maxSteps}`,
        this.steps,
        this.limits.maxSteps,
      );
    }
    if (Date.now() >= this.deadline) {
      throw new TimeoutExceeded(
        `Execution timed out after ${this.limits.timeoutMs}ms`,
        this.limits.timeoutMs,
      );
    }
  }

  /** Charge memory-allocation units. */
  chargeMemory(units: number): void {
    this.memory += units;
    if (this.memory > this.limits.maxMemory) {
      throw new MemoryLimitExceeded(
        `Memory budget exhausted: ${this.memory} > ${this.limits.maxMemory} units`,
        this.memory,
        this.limits.maxMemory,
      );
    }
  }

  get maxCallDepth(): number {
    return this.limits.maxCallDepth;
  }

  get stepsUsed(): number {
    return this.steps;
  }

  get memoryUsed(): number {
    return this.memory;
  }
}
