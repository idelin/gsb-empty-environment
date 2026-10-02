import { evaluateDetailed, type BuiltinFn, type Value } from './evaluator.ts';

export interface SandboxOptions {
  /** Maximum instruction steps per evaluation (default 1000). */
  maxSteps?: number;
  /** Maximum bytes the expression may allocate (default 1 MB). */
  maxMemoryBytes?: number;
  /** Wall-clock limit per evaluation in ms (default 100). */
  timeoutMs?: number;
  /** Maximum nesting depth of sandboxed eval() calls (default 5000). */
  maxDepth?: number;
  /** Extra tenant-supplied pure functions to whitelist. */
  functions?: Record<string, BuiltinFn>;
}

export interface RunResult {
  value: Value;
  stepsUsed: number;
  memoryUsed: number;
}

/**
 * A safe evaluator for user-submitted boolean / arithmetic rule expressions.
 *
 * Safety model:
 *  - Expressions are lexed/parsed by a dedicated grammar: no member access,
 *    no assignment, no indexing, no statements — `.` cannot even be tokenized.
 *  - The AST is validated before running: every identifier and every called
 *    function must be explicitly whitelisted.
 *  - Evaluation charges each AST node against a shared instruction budget
 *    (default 1000 steps), a memory ledger, and a wall-clock deadline.
 *    Exceeding any limit aborts immediately with BudgetExceeded /
 *    MemoryExceeded / EvaluationTimeoutError.
 */
export class RuleSandbox {
  private readonly options: SandboxOptions;

  constructor(options: SandboxOptions = {}) {
    this.options = options;
  }

  /** Evaluate an expression and return only the value. */
  evaluate(source: string, variables: Record<string, Value> = {}): Value {
    return this.run(source, variables).value;
  }

  /** Evaluate an expression and return the value plus consumed resources. */
  run(source: string, variables: Record<string, Value> = {}): RunResult {
    const { value, stepsUsed, memoryUsed } = evaluateDetailed(source, {
      maxSteps: this.options.maxSteps,
      maxMemoryBytes: this.options.maxMemoryBytes,
      timeoutMs: this.options.timeoutMs,
      maxDepth: this.options.maxDepth,
      functions: this.options.functions,
      variables,
    });
    return { value, stepsUsed, memoryUsed };
  }
}
