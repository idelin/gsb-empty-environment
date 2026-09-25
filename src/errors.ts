export class QuotaError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export interface Shortage {
  readonly account: string;
  readonly dimension: string;
  readonly requested: number;
  readonly available: number;
}

export class InsufficientQuotaError extends QuotaError {
  readonly shortages: readonly Shortage[];
  constructor(shortages: readonly Shortage[]) {
    const detail = shortages
      .map((s) => `${s.account}/${s.dimension}: need ${s.requested}, have ${s.available}`)
      .join('; ');
    super('INSUFFICIENT_QUOTA', `insufficient quota: ${detail}`);
    this.shortages = shortages;
  }
}

export class IdempotencyConflictError extends QuotaError {
  constructor(operationId: string) {
    super(
      'IDEMPOTENCY_CONFLICT',
      `operation "${operationId}" was already submitted with a different request`,
    );
  }
}

export class UnknownOperationError extends QuotaError {
  constructor(operationId: string) {
    super('UNKNOWN_OPERATION', `unknown operation "${operationId}"`);
  }
}

export class StalePermitError extends QuotaError {
  constructor(operationId: string, permitVersion: number, currentVersion: number) {
    super(
      'STALE_PERMIT',
      `permit for operation "${operationId}" was issued under policy v${permitVersion} ` +
        `but current policy is v${currentVersion}; reservation rolled back`,
    );
  }
}

export class PermitMismatchError extends QuotaError {
  constructor(operationId: string) {
    super('PERMIT_MISMATCH', `permit does not match operation "${operationId}"`);
  }
}

export class PolicyDeniedError extends QuotaError {
  constructor(operationId: string, policyVersion: number) {
    super(
      'POLICY_DENIED',
      `operation "${operationId}" denied by policy v${policyVersion}`,
    );
  }
}

export class InvalidStateError extends QuotaError {
  constructor(code: string, message: string) {
    super(code, message);
  }
}

export class InvalidPolicyError extends QuotaError {
  constructor(message: string) {
    super('INVALID_POLICY', message);
  }
}

export class InvalidRequestError extends QuotaError {
  constructor(message: string) {
    super('INVALID_REQUEST', message);
  }
}
