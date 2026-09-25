import type { Attributes } from './policy.ts';

/** 补偿台账：保证补偿重试幂等，并保留补偿失败后的待处理状态。 */
export interface CompensationLedger {
  /** 已完成的补偿集合（幂等键）。 */
  readonly compensated: ReadonlySet<string>;
  /** 仍处于待补偿状态的义务，按应执行顺序排列。 */
  readonly pending: readonly string[];
  markCompensated(id: string): void;
  setPending(ids: readonly string[]): void;
}

export class InMemoryCompensationLedger implements CompensationLedger {
  private readonly done = new Set<string>();
  private pendingIds: string[] = [];
  get compensated(): ReadonlySet<string> {
    return this.done;
  }
  get pending(): readonly string[] {
    return this.pendingIds;
  }
  markCompensated(id: string): void {
    this.done.add(id);
    this.pendingIds = this.pendingIds.filter((p) => p !== id);
  }
  setPending(ids: readonly string[]): void {
    this.pendingIds = [...ids];
  }
}

export interface ObligationContext {
  readonly attributes: Attributes;
  /** 当前执行/补偿尝试次数（从 1 开始），供幂等实现使用。 */
  readonly attempt: number;
  readonly ledger: CompensationLedger;
}

interface BaseObligation {
  readonly id: string;
  /** 依赖的其他义务 id，拓扑排序决定执行顺序。 */
  readonly dependsOn?: readonly string[];
}

/** 可回滚义务：在提交阶段之前执行，失败时按逆序补偿。 */
export interface RollbackableObligation extends BaseObligation {
  readonly kind: 'rollbackable';
  execute(ctx: ObligationContext): void | Promise<void>;
  /** 必须幂等：同一义务可能被重试多次，效果只能发生一次。 */
  compensate(ctx: ObligationContext): void | Promise<void>;
}

/** 不可回滚义务：只允许在明确的提交阶段执行，没有补偿。 */
export interface CommitObligation extends BaseObligation {
  readonly kind: 'commit';
  execute(ctx: ObligationContext): void | Promise<void>;
}

export type Obligation = RollbackableObligation | CommitObligation;

export interface ExecutionPlan {
  /** 提交阶段之前，按依赖拓扑序排列的可回滚义务。 */
  readonly rollbackPhase: readonly RollbackableObligation[];
  /** 明确提交点之后，按依赖拓扑序排列的不可回滚义务。 */
  readonly commitPhase: readonly CommitObligation[];
}

function topoSort<T extends BaseObligation>(items: readonly T[]): T[] {
  const byId = new Map(items.map((o) => [o.id, o]));
  const visited = new Set<string>();
  const visiting = new Set<string>();
  const ordered: T[] = [];
  const visit = (node: T): void => {
    if (visited.has(node.id)) return;
    if (visiting.has(node.id)) {
      throw new Error(`dependency cycle detected at obligation "${node.id}"`);
    }
    visiting.add(node.id);
    for (const dep of node.dependsOn ?? []) {
      const target = byId.get(dep);
      if (target) visit(target);
    }
    visiting.delete(node.id);
    visited.add(node.id);
    ordered.push(node);
  };
  for (const item of items) visit(item);
  return ordered;
}

/**
 * 构建执行计划：
 *  - 可回滚义务按依赖拓扑序进入回滚阶段；
 *  - 不可回滚义务一律排在明确的提交阶段；
 *  - 可回滚义务不允许依赖不可回滚义务（否则提交点将失去意义）。
 */
export function buildPlan(obligations: readonly Obligation[]): ExecutionPlan {
  const ids = new Set<string>();
  for (const o of obligations) {
    if (ids.has(o.id)) throw new Error(`duplicate obligation id "${o.id}"`);
    ids.add(o.id);
  }
  const byId = new Map(obligations.map((o) => [o.id, o]));
  for (const o of obligations) {
    for (const dep of o.dependsOn ?? []) {
      if (!byId.has(dep)) throw new Error(`obligation "${o.id}" depends on unknown obligation "${dep}"`);
      if (o.kind === 'rollbackable' && byId.get(dep)!.kind === 'commit') {
        throw new Error(
          `rollbackable obligation "${o.id}" cannot depend on non-rollbackable obligation "${dep}"`,
        );
      }
    }
  }
  const rollbackPhase = topoSort(obligations.filter((o): o is RollbackableObligation => o.kind === 'rollbackable'));
  const commitPhase = topoSort(obligations.filter((o): o is CommitObligation => o.kind === 'commit'));
  return { rollbackPhase, commitPhase };
}
