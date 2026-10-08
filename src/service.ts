import type { Expr, Value } from './ast.ts';
import {
  CancelToken,
  EvalFailure,
  evaluate,
} from './interpreter.ts';
import type { Budget, EvalOutcome, SpaceLedger } from './interpreter.ts';

export interface RuleRequest {
  tenantId: string;
  ast: Expr;
  variables?: Record<string, unknown>;
  budget: Budget;
}

export interface RequestHandle {
  id: number;
  promise: Promise<EvalOutcome>;
  cancel(): void;
}

interface TenantState {
  used: number; // in-flight array slots reserved by all live requests of this tenant
  limit: number;
}

export interface RuleServiceOptions {
  defaultTenantSpaceLimit?: number;
  sliceSteps?: number;
}

export class RuleService {
  private readonly defaultTenantSpaceLimit: number;
  private readonly sliceSteps: number | undefined;
  private readonly tenants = new Map<string, TenantState>();
  private nextId = 1;

  constructor(options: RuleServiceOptions = {}) {
    this.defaultTenantSpaceLimit = options.defaultTenantSpaceLimit ?? 1024;
    this.sliceSteps = options.sliceSteps;
  }

  setTenantSpaceLimit(tenantId: string, limit: number): void {
    this.tenant(tenantId).limit = limit;
  }

  tenantSpaceInUse(tenantId: string): number {
    return this.tenants.get(tenantId)?.used ?? 0;
  }

  submit(request: RuleRequest): RequestHandle {
    const tenant = this.tenant(request.tenantId);
    const token = new CancelToken();
    let reserved = 0;

    // Per-request ledger: every reservation is tracked so it can be fully
    // released when the request finishes, fails or is cancelled.
    const ledger: SpaceLedger = {
      reserve(n: number, path: string): void {
        if (tenant.used + n > tenant.limit) {
          throw new EvalFailure({
            kind: 'SpaceExhausted',
            path,
            message: `tenant '${request.tenantId}' in-flight space limit exceeded (limit ${tenant.limit})`,
          });
        }
        tenant.used += n;
        reserved += n;
      },
    };

    const promise = evaluate({
      ast: request.ast,
      variables: request.variables,
      budget: request.budget,
      token,
      ledger,
      sliceSteps: this.sliceSteps,
    }).finally(() => {
      tenant.used -= reserved; // release on success, failure and cancellation alike
    });

    const id = this.nextId++;
    return {
      id,
      promise,
      cancel: () => token.cancel(),
    };
  }

  private tenant(tenantId: string): TenantState {
    let t = this.tenants.get(tenantId);
    if (!t) {
      t = { used: 0, limit: this.defaultTenantSpaceLimit };
      this.tenants.set(tenantId, t);
    }
    return t;
  }
}

export type { EvalOutcome, Value };
