/** Base class for all sandbox-related errors. */
export class SandboxError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** The expression failed lexical / syntactic / semantic validation. */
export class ValidationError extends SandboxError {}

/** The expression tried to call a function that is not whitelisted. */
export class UnauthorizedFunctionError extends ValidationError {
  readonly functionName: string;
  constructor(functionName: string) {
    super(`call to unauthorized function '${functionName}' is forbidden`);
    this.functionName = functionName;
  }
}

/** The expression referenced a variable that is not in scope. */
export class UnknownIdentifierError extends ValidationError {
  readonly identifier: string;
  constructor(identifier: string) {
    super(`unknown identifier '${identifier}'`);
    this.identifier = identifier;
  }
}

/** The instruction-step budget was exhausted; execution was aborted. */
export class BudgetExceeded extends SandboxError {
  readonly limit: number;
  constructor(limit: number) {
    super(`BudgetExceeded: execution aborted after exceeding the ${limit}-step instruction budget`);
    this.limit = limit;
  }
}

/** The memory allocation budget was exhausted; execution was aborted. */
export class MemoryExceeded extends SandboxError {
  readonly limit: number;
  constructor(limit: number) {
    super(`MemoryExceeded: execution aborted after exceeding the ${limit}-byte memory budget`);
    this.limit = limit;
  }
}

/** The wall-clock deadline was reached; execution was aborted. */
export class EvaluationTimeoutError extends SandboxError {
  readonly timeoutMs: number;
  constructor(timeoutMs: number) {
    super(`EvaluationTimeout: execution aborted after ${timeoutMs}ms`);
    this.timeoutMs = timeoutMs;
  }
}

/** The maximum eval() nesting depth was reached. */
export class DepthExceededError extends SandboxError {
  readonly maxDepth: number;
  constructor(maxDepth: number) {
    super(`DepthExceeded: maximum evaluation nesting depth (${maxDepth}) reached`);
    this.maxDepth = maxDepth;
  }
}

/** A runtime failure while evaluating a validated expression. */
export class EvaluationError extends SandboxError {}

/** A tenant exhausted its quota in the QuotaGuard. */
export class QuotaExceededError extends SandboxError {
  readonly tenantId: string;
  readonly reason: string;
  constructor(tenantId: string, reason: string) {
    super(`QuotaExceeded for tenant '${tenantId}': ${reason}`);
    this.tenantId = tenantId;
    this.reason = reason;
  }
}
