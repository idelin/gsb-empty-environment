/**
 * 确定性异常注入器: 模拟突发断电、磁盘满、网络丢包。
 * 用 xorshift32 伪随机序列 + 可编程触发点, 保证测试可复现。
 */

import { InjectedFaultError } from './errors.ts';

export type FaultKind = 'power-loss' | 'disk-full' | 'packet-loss';

export interface FaultPlan {
  /** 触发概率 0..1 (默认 0, 即不随机触发)。 */
  probability?: number;
  /** 在第 N 次(1-based)机会时必然触发一次。 */
  fireOnCalls?: number[];
}

export class FaultInjector {
  private state: number;
  private readonly plans = new Map<FaultKind, FaultPlan>();
  private readonly counters = new Map<FaultKind, number>();
  /** 已触发记录, 供测试断言。 */
  readonly firedLog: FaultKind[] = [];

  constructor(seed = 0x9e3779b9) {
    this.state = seed >>> 0 || 1;
  }

  arm(kind: FaultKind, plan: FaultPlan): void {
    this.plans.set(kind, plan);
  }

  disarm(kind: FaultKind): void {
    this.plans.delete(kind);
  }

  disarmAll(): void {
    this.plans.clear();
  }

  private nextRandom(): number {
    let x = this.state;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.state = x >>> 0;
    return this.state / 0x100000000;
  }

  /** 询问是否应在此刻注入故障; 若注入则记录并返回 true。 */
  shouldFire(kind: FaultKind): boolean {
    const plan = this.plans.get(kind);
    if (!plan) return false;
    const n = (this.counters.get(kind) ?? 0) + 1;
    this.counters.set(kind, n);
    let fire = false;
    if (plan.fireOnCalls?.includes(n)) fire = true;
    else if (plan.probability && this.nextRandom() < plan.probability) fire = true;
    if (fire) this.firedLog.push(kind);
    return fire;
  }

  /** 若触发则抛出对应故障异常。 */
  maybeThrow(kind: FaultKind, detail: string): void {
    if (this.shouldFire(kind)) {
      throw new InjectedFaultError(kind, detail);
    }
  }
}
