import {
  OperationConflictError,
  PolicyViolationError,
  StalePolicyError,
  UnknownOperationError,
} from "./errors.ts";
import { QuotaLedger, type DimensionState, type Requirements } from "./ledger.ts";
import {
  definePolicy,
  evaluatePolicy,
  type Attributes,
  type Policy,
} from "./policy.ts";

export type PermitStatus = "active" | "cancelled" | "failed";

export interface Permit {
  readonly operationId: string;
  readonly requirements: Requirements;
  readonly policyVersion: number;
  readonly status: PermitStatus;
}

export interface AcquireRequest {
  /** Idempotency key: one logical operation, deducted at most once. */
  readonly operationId: string;
  /** Amounts to debit per dimension; all must fit or none are debited. */
  readonly requirements: Requirements;
  /** Explicit attributes evaluated against the active policy. */
  readonly attributes?: Attributes;
  /** Must equal the current policy version. */
  readonly policyVersion: number;
}

interface PermitRecord {
  readonly operationId: string;
  readonly requirements: Record<string, number>;
  readonly fingerprint: string;
  readonly policyVersion: number;
  status: PermitStatus;
}

/** Canonical, order-independent fingerprint of a requirements map. */
function fingerprint(requirements: Requirements): string {
  const sorted = Object.keys(requirements)
    .sort()
    .map((k) => [k, requirements[k]] as const);
  return JSON.stringify(sorted);
}

function toPermit(record: PermitRecord): Permit {
  return Object.freeze({
    operationId: record.operationId,
    requirements: Object.freeze({ ...record.requirements }),
    policyVersion: record.policyVersion,
    status: record.status,
  });
}

/**
 * Quota service: atomic multi-dimensional deduction with idempotent permits,
 * full rollback on cancel/fail, and version-gated read-only policies.
 */
export class QuotaService {
  readonly #ledger: QuotaLedger;
  readonly #permits = new Map<string, PermitRecord>();
  #policy: Readonly<Policy>;

  constructor(capacities: Readonly<Record<string, number>>, initialPolicy: Policy) {
    this.#ledger = new QuotaLedger(capacities);
    this.#policy = definePolicy(initialPolicy);
  }

  get policyVersion(): number {
    return this.#policy.version;
  }

  /**
   * Install a new policy version. The version must increase; any permit
   * submitted afterwards with an older version is rejected.
   */
  setPolicy(policy: Policy): void {
    const next = definePolicy(policy);
    if (next.version <= this.#policy.version) {
      throw new RangeError(
        `policy version must increase (current ${this.#policy.version}, got ${next.version})`,
      );
    }
    this.#policy = next;
  }

  /**
   * Acquire quota across all requested dimensions.
   *
   * Idempotency: replaying the same operationId with the same requirements
   * returns the original permit without debiting again; replaying it with
   * different requirements raises OperationConflictError.
   *
   * Atomicity: if any dimension is short (e.g. user quota fits but resource
   * quota does not), nothing is debited on any dimension.
   */
  acquire(request: AcquireRequest): Permit {
    const { operationId, requirements, policyVersion } = request;
    const attributes = request.attributes ?? {};

    const existing = this.#permits.get(operationId);
    if (existing !== undefined) {
      if (existing.fingerprint !== fingerprint(requirements)) {
        throw new OperationConflictError(operationId);
      }
      // Idempotent replay: same id + same requirements => no second deduction.
      return toPermit(existing);
    }

    if (policyVersion !== this.#policy.version) {
      throw new StalePolicyError(policyVersion, this.#policy.version);
    }
    if (!evaluatePolicy(this.#policy, attributes)) {
      throw new PolicyViolationError(this.#policy.version);
    }

    // Throws InsufficientQuotaError without side effects when any dimension is short.
    this.#ledger.deduct(requirements);

    const record: PermitRecord = {
      operationId,
      requirements: { ...requirements },
      fingerprint: fingerprint(requirements),
      policyVersion,
      status: "active",
    };
    this.#permits.set(operationId, record);
    return toPermit(record);
  }

  /** Roll back every dimension of an active permit (caller-initiated cancel). */
  cancel(operationId: string): Permit {
    return this.#close(operationId, "cancelled");
  }

  /** Roll back every dimension of an active permit (downstream failure). */
  fail(operationId: string): Permit {
    return this.#close(operationId, "failed");
  }

  #close(operationId: string, status: "cancelled" | "failed"): Permit {
    const record = this.#permits.get(operationId);
    if (record === undefined || record.status !== "active") {
      throw new UnknownOperationError(operationId);
    }
    this.#ledger.release(record.requirements);
    record.status = status;
    return toPermit(record);
  }

  /** Look up a permit without changing state. */
  getPermit(operationId: string): Permit | undefined {
    const record = this.#permits.get(operationId);
    return record === undefined ? undefined : toPermit(record);
  }

  snapshot(): Readonly<Record<string, DimensionState>> {
    return this.#ledger.snapshot();
  }
}
