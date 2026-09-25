import { evaluatePolicy, type Attributes, type Policy } from './policy.ts';
import {
  buildPlan,
  InMemoryCompensationLedger,
  type CompensationLedger,
  type Obligation,
  type ObligationContext,
  type RollbackableObligation,
} from './obligations.ts';

export type ExecutionResult =
  | { readonly status: 'denied'; readonly reasons: readonly string[] }
  | {
      readonly status: 'compensated';
      readonly failedObligation: string;
      readonly error: unknown;
      readonly compensated: readonly string[];
    }
  | {
      readonly status: 'pending_compensation';
      readonly failedObligation: string;
      readonly error: unknown;
      readonly pending: readonly string[];
    }
  | {
      readonly status: 'committed';
      readonly executed: readonly string[];
      readonly committed: readonly string[];
    };

export interface ExecutorOptions {
  /** 单个补偿的最大尝试次数（默认 3）。补偿必须幂等，重试安全。 */
  readonly maxCompensationAttempts?: number;
  /** 补偿台账；传入同一台账可在进程重启后继续处理待补偿状态。 */
  readonly ledger?: CompensationLedger;
}

export class ObligationExecutor {
  private readonly policy: Policy;
  private readonly obligations: readonly Obligation[];
  private readonly maxCompensationAttempts: number;
  private readonly ledger: CompensationLedger;

  constructor(
    policy: Policy,
    obligations: readonly Obligation[],
    options: ExecutorOptions = {},
  ) {
    this.policy = policy;
    this.obligations = obligations;
    this.maxCompensationAttempts = options.maxCompensationAttempts ?? 3;
    this.ledger = options.ledger ?? new InMemoryCompensationLedger();
  }

  /**
   * 流程：策略允许 -> 回滚阶段（依赖拓扑序）-> 明确提交点 -> 提交阶段。
   * 只有在提交阶段全部完成后才返回 committed，绝不提前返回最终成功。
   */
  async run(attributes: Attributes): Promise<ExecutionResult> {
    const decision = evaluatePolicy(this.policy, attributes);
    if (decision.decision === 'deny') {
      return { status: 'denied', reasons: decision.reasons };
    }

    const plan = buildPlan(this.obligations);
    const executed: RollbackableObligation[] = [];

    for (const obligation of plan.rollbackPhase) {
      try {
        await obligation.execute(this.ctx(attributes, 1));
        executed.push(obligation);
      } catch (error) {
        // 失败：对已执行义务按逆序补偿。
        return this.compensateAll(executed, obligation.id, error, attributes);
      }
    }

    // ---- 明确提交点：以下义务不可回滚 ----
    const committed: string[] = [];
    for (const obligation of plan.commitPhase) {
      await obligation.execute(this.ctx(attributes, 1));
      committed.push(obligation.id);
    }

    return {
      status: 'committed',
      executed: executed.map((o) => o.id),
      committed,
    };
  }

  /** 对遗留的待补偿状态继续补偿（例如进程重启后恢复）。 */
  async resumePendingCompensation(attributes: Attributes): Promise<ExecutionResult | null> {
    const pending = this.ledger.pending;
    if (pending.length === 0) return null;
    const byId = new Map(
      this.obligations
        .filter((o): o is RollbackableObligation => o.kind === 'rollbackable')
        .map((o) => [o.id, o]),
    );
    const targets = pending.map((id) => {
      const obligation = byId.get(id);
      if (!obligation) throw new Error(`pending obligation "${id}" not found`);
      return obligation;
    });
    return this.compensateAll(targets, targets[0]?.id ?? '', undefined, attributes);
  }

  private ctx(attributes: Attributes, attempt: number): ObligationContext {
    return { attributes, attempt, ledger: this.ledger };
  }

  private async compensateAll(
    executed: readonly RollbackableObligation[],
    failedObligation: string,
    error: unknown,
    attributes: Attributes,
  ): Promise<ExecutionResult> {
    const reverse = [...executed].reverse();
    this.ledger.setPending(reverse.map((o) => o.id));
    for (const obligation of reverse) {
      if (this.ledger.compensated.has(obligation.id)) {
        this.ledger.markCompensated(obligation.id);
        continue;
      }
      let lastError: unknown;
      let ok = false;
      for (let attempt = 1; attempt <= this.maxCompensationAttempts; attempt++) {
        try {
          await obligation.compensate(this.ctx(attributes, attempt));
          ok = true;
          break;
        } catch (compensationError) {
          lastError = compensationError;
        }
      }
      if (!ok) {
        // 补偿失败：保留待处理状态，不返回最终态。
        return {
          status: 'pending_compensation',
          failedObligation,
          error,
          pending: this.ledger.pending,
        };
      }
      this.ledger.markCompensated(obligation.id);
      void lastError;
    }
    return {
      status: 'compensated',
      failedObligation,
      error,
      compensated: reverse.map((o) => o.id),
    };
  }
}
