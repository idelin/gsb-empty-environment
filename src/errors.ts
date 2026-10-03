/** Thrown when the expression text cannot be tokenized or parsed. */
export class ParseError extends Error {
  override readonly name = 'ParseError';
}

/** Thrown when the AST violates the sandbox security policy. */
export class ValidationError extends Error {
  override readonly name = 'ValidationError';
}

/** Thrown when the instruction-step budget is exhausted. */
export class BudgetExceeded extends Error {
  override readonly name = 'BudgetExceeded';
  readonly stepsUsed: number;
  readonly maxSteps: number;
  constructor(
    message: string,
    stepsUsed: number,
    maxSteps: number,
  ) {
    super(message);
    this.stepsUsed = stepsUsed;
    this.maxSteps = maxSteps;
  }
}

/** Thrown when the memory-allocation budget is exhausted. */
export class MemoryLimitExceeded extends Error {
  override readonly name = 'MemoryLimitExceeded';
  readonly memoryUsed: number;
  readonly maxMemory: number;
  constructor(
    message: string,
    memoryUsed: number,
    maxMemory: number,
  ) {
    super(message);
    this.memoryUsed = memoryUsed;
    this.maxMemory = maxMemory;
  }
}

/** Thrown when the wall-clock timeout elapses mid-execution. */
export class TimeoutExceeded extends Error {
  override readonly name = 'TimeoutExceeded';
  readonly timeoutMs: number;
  constructor(message: string, timeoutMs: number) {
    super(message);
    this.timeoutMs = timeoutMs;
  }
}
