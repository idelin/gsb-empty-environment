export class QuotaError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

/** All-or-nothing check failed: at least one dimension lacks capacity. */
export class InsufficientQuotaError extends QuotaError {
  readonly shortages: Readonly<Record<string, { requested: number; available: number }>>;
  constructor(shortages: Record<string, { requested: number; available: number }>) {
    super(
      "INSUFFICIENT_QUOTA",
      `Insufficient quota on dimension(s): ${Object.keys(shortages).join(", ")}`,
    );
    this.shortages = Object.freeze({ ...shortages });
  }
}

/** Same operation id was submitted with different requirements. */
export class OperationConflictError extends QuotaError {
  constructor(operationId: string) {
    super(
      "OPERATION_CONFLICT",
      `Operation "${operationId}" was already submitted with different requirements`,
    );
  }
}

/** Permit references a policy version that is no longer current. */
export class StalePolicyError extends QuotaError {
  constructor(submitted: number, current: number) {
    super(
      "STALE_POLICY_VERSION",
      `Permit targets policy version ${submitted}, but current version is ${current}`,
    );
  }
}

/** Explicit attributes do not satisfy the active policy. */
export class PolicyViolationError extends QuotaError {
  constructor(version: number) {
    super("POLICY_VIOLATION", `Attributes do not satisfy policy version ${version}`);
  }
}

/** Operation id has no active permit to cancel/fail. */
export class UnknownOperationError extends QuotaError {
  constructor(operationId: string) {
    super("UNKNOWN_OPERATION", `No active permit for operation "${operationId}"`);
  }
}
