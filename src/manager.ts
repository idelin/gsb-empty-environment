import { createHash } from 'node:crypto';
import {
  IdempotencyConflictError,
  InsufficientQuotaError,
  InvalidRequestError,
  InvalidStateError,
  PermitMismatchError,
  PolicyDeniedError,
  StalePermitError,
  UnknownOperationError,
} from './errors.ts';
import {
  createPolicy,
  evaluateRule,
  freezeAttributes,
  type AttributeSet,
  type Policy,
  type Rule,
} from './policy.ts';
import { normalizeLegs, QuotaStore, type DeductionLeg } from './quota.ts';

export interface AcquireRequest {
  readonly operationId: string;
  readonly legs: readonly DeductionLeg[];
  readonly attributes?: Readonly<Record<string, unknown>>;
}

export interface Permit {
  readonly operationId: string;
  readonly policyVersion: number;
  readonly requestHash: string;
  readonly issuedAt: number;
}

export type OperationStatus = 'reserved' | 'committed' | 'rolled-back';

export type AcquireResult =
  | { readonly status: 'reserved'; readonly permit: Permit }
  | { readonly status: 'committed'; readonly permit: Permit }
  | { readonly status: 'rolled-back' };

interface OperationRecord {
  readonly operationId: string;
  readonly requestHash: string;
  readonly legs: readonly DeductionLeg[];
  readonly policyVersion: number;
  readonly permit: Permit;
  status: OperationStatus;
}

function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  const entries = Object.keys(value as Record<string, unknown>)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalize((value as Record<string, unknown>)[key])}`);
  return `{${entries.join(',')}}`;
}

function hashRequest(request: unknown): string {
  return createHash('sha256').update(canonicalize(request)).digest('hex');
}

function deepFreeze<T>(value: T, seen = new Set<object>()): T {
  if (value !== null && typeof value === 'object' && !seen.has(value)) {
    seen.add(value as object);
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key], seen);
    }
    Object.freeze(value);
  }
  return value;
}

export class QuotaManager {
  readonly store = new QuotaStore();
  readonly #operations = new Map<string, OperationRecord>();
  readonly #policies = new Map<number, Policy>();
  #current: Policy;

  constructor(policy: { readonly name: string; readonly rule: Rule }) {
    this.#current = createPolicy(1, policy.name, policy.rule);
    this.#policies.set(1, this.#current);
  }

  get currentPolicy(): Policy {
    return this.#current;
  }

  policyVersion(version: number): Policy | undefined {
    return this.#policies.get(version);
  }

  updatePolicy(name: string, rule: Rule): Policy {
    const next = createPolicy(this.#current.version + 1, name, rule);
    this.#policies.set(next.version, next);
    this.#current = next;
    return next;
  }

  acquire(request: AcquireRequest): AcquireResult {
    if (typeof request?.operationId !== 'string' || request.operationId.length === 0) {
      throw new InvalidRequestError('operationId must be a non-empty string');
    }
    const legs = normalizeLegs(request.legs);
    const attributes: AttributeSet = freezeAttributes(request.attributes ?? {});
    const requestHash = hashRequest({ legs, attributes });

    const existing = this.#operations.get(request.operationId);
    if (existing) {
      if (existing.requestHash !== requestHash) {
        throw new IdempotencyConflictError(request.operationId);
      }
      switch (existing.status) {
        case 'reserved':
          return { status: 'reserved', permit: existing.permit };
        case 'committed':
          return { status: 'committed', permit: existing.permit };
        default:
          return { status: 'rolled-back' };
      }
    }

    if (!evaluateRule(this.#current.rule, attributes)) {
      throw new PolicyDeniedError(request.operationId, this.#current.version);
    }

    const shortages = this.store.checkAll(legs);
    if (shortages.length > 0) {
      throw new InsufficientQuotaError(shortages);
    }

    this.store.deductAll(legs);
    const permit: Permit = deepFreeze({
      operationId: request.operationId,
      policyVersion: this.#current.version,
      requestHash,
      issuedAt: Date.now(),
    });
    this.#operations.set(request.operationId, {
      operationId: request.operationId,
      requestHash,
      legs,
      policyVersion: this.#current.version,
      permit,
      status: 'reserved',
    });
    return { status: 'reserved', permit };
  }

  commit(operationId: string, permit: Permit): { readonly status: 'committed' } {
    const record = this.#requireRecord(operationId);
    if (
      permit?.operationId !== record.operationId ||
      permit.requestHash !== record.requestHash ||
      permit.policyVersion !== record.policyVersion
    ) {
      throw new PermitMismatchError(operationId);
    }
    if (record.status === 'committed') {
      return { status: 'committed' };
    }
    if (record.status === 'rolled-back') {
      throw new InvalidStateError(
        'OPERATION_ROLLED_BACK',
        `operation "${operationId}" was rolled back and cannot be committed`,
      );
    }
    if (record.policyVersion !== this.#current.version) {
      this.#rollback(record);
      throw new StalePermitError(operationId, record.policyVersion, this.#current.version);
    }
    record.status = 'committed';
    return { status: 'committed' };
  }

  cancel(operationId: string): { readonly status: 'rolled-back' } {
    return this.#rollback(this.#requireRecord(operationId));
  }

  fail(operationId: string): { readonly status: 'rolled-back' } {
    return this.#rollback(this.#requireRecord(operationId));
  }

  statusOf(operationId: string): OperationStatus | undefined {
    return this.#operations.get(operationId)?.status;
  }

  #requireRecord(operationId: string): OperationRecord {
    const record = this.#operations.get(operationId);
    if (!record) throw new UnknownOperationError(operationId);
    return record;
  }

  #rollback(record: OperationRecord): { readonly status: 'rolled-back' } {
    switch (record.status) {
      case 'rolled-back':
        return { status: 'rolled-back' };
      case 'committed':
        throw new InvalidStateError(
          'OPERATION_COMMITTED',
          `operation "${record.operationId}" is committed and cannot be rolled back`,
        );
      default:
        this.store.restoreAll(record.legs);
        record.status = 'rolled-back';
        return { status: 'rolled-back' };
    }
  }
}
