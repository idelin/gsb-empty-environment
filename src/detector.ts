import type { Clock } from './clock.ts';
import type { Evidence, GuardConfig } from './types.ts';
import { RuleId, DenyScope } from './types.ts';

interface FailureEvent {
  t: number;
  source: string;
}

/** 检测器发现的、需要调用方落地为锁定的信号。 */
export interface DetectorSignal {
  rule: RuleId;
  scope: DenyScope;
  subject: string;
  reason: string;
  evidence: Evidence;
}

interface WindowStats {
  failures: number;
  distinctSources: number;
}

/**
 * 轮换来源检测。
 *
 * 两个信号：
 * 1. 低速填充（全局）：检测窗口内，唯一来源数与失败次数同时达到阈值。
 *    攻击者把请求摊薄到限速阈值之下、并不断轮换来源时，单来源 / 单账号
 *    限速都不会触发，但全局聚合视图可以识别。
 * 2. 单账号多来源喷洒：同一账号在窗口内被超过阈值的不同来源失败尝试。
 *
 * 检测器只负责聚合与判定，锁定的落地由 AuthGuard 经 LockRegistry 完成，
 * 已在锁定中的主体不会重复产生事件（由调用方去重）。
 */
export class RotatingSourceDetector {
  private readonly globalEvents: FailureEvent[] = [];
  private readonly accountEvents = new Map<string, FailureEvent[]>();

  constructor(
    private readonly clock: Clock,
    private readonly config: GuardConfig,
  ) {}

  private prune(events: FailureEvent[]): FailureEvent[] {
    const cutoff = this.clock.now() - this.config.fillWindowMs;
    return events.filter((event) => event.t >= cutoff);
  }

  private stats(events: FailureEvent[]): WindowStats {
    return {
      failures: events.length,
      distinctSources: new Set(events.map((event) => event.source)).size,
    };
  }

  /**
   * 登记一次失败尝试并返回新触发的信号（可能为空或多个）。
   * @param trackAccount 是否纳入单账号喷洒统计（仅存在的账号需要）。
   */
  observe(account: string, source: string, trackAccount: boolean): DetectorSignal[] {
    const now = this.clock.now();
    const signals: DetectorSignal[] = [];

    const global = this.prune(this.globalEvents);
    global.push({ t: now, source });
    this.globalEvents.length = 0;
    this.globalEvents.push(...global);

    const globalStats = this.stats(global);
    if (
      globalStats.distinctSources >= this.config.fillDistinctSources &&
      globalStats.failures >= this.config.fillMinFailures
    ) {
      signals.push({
        rule: RuleId.RotatingSourceLowSlowFill,
        scope: DenyScope.Global,
        subject: '*',
        reason:
          '轮换来源低速填充：窗口内唯一来源数与失败次数同时超过阈值，触发全局防护',
        evidence: {
          windowMs: this.config.fillWindowMs,
          distinctSources: globalStats.distinctSources,
          distinctSourceThreshold: this.config.fillDistinctSources,
          failures: globalStats.failures,
          failureThreshold: this.config.fillMinFailures,
        },
      });
    }

    if (trackAccount) {
      const accountEvents = this.prune(this.accountEvents.get(account) ?? []);
      accountEvents.push({ t: now, source });
      this.accountEvents.set(account, accountEvents);
      const accountStats = this.stats(accountEvents);
      if (accountStats.distinctSources >= this.config.accountSprayDistinctSources) {
        signals.push({
          rule: RuleId.AccountSourceSpray,
          scope: DenyScope.Account,
          subject: account,
          reason: '单账号被多个不同来源失败尝试，疑似口令喷洒 / 撞库',
          evidence: {
            account,
            windowMs: this.config.fillWindowMs,
            distinctSources: accountStats.distinctSources,
            distinctSourceThreshold: this.config.accountSprayDistinctSources,
            failures: accountStats.failures,
          },
        });
      }
    }

    return signals;
  }

  /** 当前检测窗口的全局统计（供指标采集）。 */
  globalSnapshot(): WindowStats {
    return this.stats(this.prune(this.globalEvents));
  }

  /** 各账号在当前窗口内的统计（供指标采集）。 */
  accountSnapshots(): Record<string, WindowStats> {
    const result: Record<string, WindowStats> = {};
    for (const [account, events] of this.accountEvents) {
      const fresh = this.prune(events);
      if (fresh.length > 0) {
        result[account] = this.stats(fresh);
      }
    }
    return result;
  }
}
