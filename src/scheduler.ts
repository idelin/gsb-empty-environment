/**
 * 调度器：编排派生节点的异步重算。
 * - 同一节点的并发重算去重为一次；
 * - 重算期间对依赖块持有借用（引用计数），驱逐无法释放被借用的物理块；
 * - 每次重算先向账本申请临时预留（临时重算也占额度），结束必须释放；
 * - 计算完成后校验依赖版本，过期结果直接丢弃并重来——旧结果绝不重新发布；
 * - drain/checkQuiescent 用于验证驱逐后的安全重算与资源释放。
 */

import type { Ledger, Reservation } from './ledger.ts';

export interface BorrowedValue {
  block: number;
  value: unknown;
}

/** 由宿主（ReactiveCache）实现的回调接口。 */
export interface SchedulerOps {
  depsOf(nodeId: string): string[];
  computeOf(nodeId: string): (values: unknown[]) => unknown | Promise<unknown>;
  estimateOf(nodeId: string): number;
  versionOf(id: string): number;
  readBorrowed(depId: string): Promise<BorrowedValue>;
  releaseBorrow(block: number): void;
  publish(nodeId: string, value: unknown, depVersions: Record<string, number>): void;
  onCompute(nodeId: string): void;
  onStaleDiscard(nodeId: string): void;
}

const MAX_ATTEMPTS = 100;

export class Scheduler {
  private ops: SchedulerOps;
  private ledger: Ledger;
  private inFlight = new Map<string, Promise<unknown>>();
  private borrows = new Map<number, number>();

  constructor(ops: SchedulerOps, ledger: Ledger) {
    this.ops = ops;
    this.ledger = ledger;
  }

  get active(): number {
    return this.inFlight.size;
  }

  /** 当前在途借用（供独立遍历核验作为引用根）。 */
  borrowCounts(): ReadonlyMap<number, number> {
    return this.borrows;
  }

  request(nodeId: string): Promise<unknown> {
    const existing = this.inFlight.get(nodeId);
    if (existing) return existing;
    const p = this.run(nodeId).finally(() => {
      this.inFlight.delete(nodeId);
    });
    this.inFlight.set(nodeId, p);
    return p;
  }

  private async run(nodeId: string): Promise<unknown> {
    const reservation: Reservation = this.ledger.reserve(
      Math.max(1, this.ops.estimateOf(nodeId)),
      `recompute:${nodeId}`,
    );
    try {
      for (let attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
        const deps = this.ops.depsOf(nodeId);
        const borrowed: number[] = [];
        const values: unknown[] = [];
        const depVersions: Record<string, number> = {};
        try {
          for (const dep of deps) {
            const bv = await this.ops.readBorrowed(dep);
            borrowed.push(bv.block);
            this.borrows.set(bv.block, (this.borrows.get(bv.block) ?? 0) + 1);
            values.push(bv.value);
            depVersions[dep] = this.ops.versionOf(dep);
          }
          this.ops.onCompute(nodeId);
          const result = await this.ops.computeOf(nodeId)(values);
          // 发布闸门：计算期间依赖若有变化，结果即为旧值，丢弃重算。
          let fresh = true;
          for (const dep of deps) {
            if (this.ops.versionOf(dep) !== depVersions[dep]) {
              fresh = false;
              break;
            }
          }
          if (!fresh) {
            this.ops.onStaleDiscard(nodeId);
            continue;
          }
          this.ops.publish(nodeId, result, depVersions);
          return result;
        } finally {
          for (const b of borrowed) {
            const n = (this.borrows.get(b) ?? 1) - 1;
            if (n <= 0) this.borrows.delete(b);
            else this.borrows.set(b, n);
            this.ops.releaseBorrow(b);
          }
        }
      }
      throw new Error(`recompute of "${nodeId}" did not converge after ${MAX_ATTEMPTS} attempts`);
    } finally {
      reservation.release();
    }
  }

  /** 等待全部在途重算结束。 */
  async drain(): Promise<void> {
    while (this.inFlight.size > 0) {
      await Promise.all([...this.inFlight.values()]);
    }
  }

  /** 静默性检查：返回违规描述列表，空数组表示资源全部正确释放。 */
  checkQuiescent(): string[] {
    const violations: string[] = [];
    if (this.inFlight.size > 0) violations.push(`${this.inFlight.size} recompute task(s) still in flight`);
    if (this.borrows.size > 0) violations.push(`${this.borrows.size} borrowed block(s) not released`);
    if (this.ledger.pendingReservedBytes > 0) {
      violations.push(`${this.ledger.pendingReservedBytes}B of recompute reservation(s) not released`);
    }
    return violations;
  }
}
