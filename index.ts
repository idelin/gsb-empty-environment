export type SourceType = "navigation" | "event";

export interface Trigger {
  /** 来源标识（同一来源共享一份预算与报告配额）。 */
  sourceId: string;
  sourceType: SourceType;
  /** 去重键：同一来源内同一 dedupKey 只计一次。 */
  dedupKey: string;
  /** 事件级触发数据，按来源类型截断：导航 3 位、事件 1 位。 */
  triggerData: number;
  /** 聚合贡献值，必须为非负整数。 */
  value: number;
}

export type RejectReason =
  | "duplicate_dedup_key"
  | "budget_exceeded"
  | "report_limit_reached"
  | "invalid_trigger";

export interface RegisterResult {
  accepted: boolean;
  reason?: RejectReason;
  /** 截断后的触发数据（拒绝时同样返回截断结果，便于调用方记录）。 */
  triggerData: number;
  /** 该来源剩余预算。 */
  remainingBudget: number;
  /** 该来源已用报告数。 */
  reportCount: number;
}

interface SourceLimits {
  triggerDataBits: number;
  maxReports: number;
}

const SOURCE_LIMITS: Record<SourceType, SourceLimits> = {
  navigation: { triggerDataBits: 3, maxReports: 3 },
  event: { triggerDataBits: 1, maxReports: 1 },
};

interface SourceState {
  sum: number;
  reportCount: number;
  dedupKeys: Set<string>;
}

export class ContributionBudgetLedger {
  /** 每个来源的聚合贡献总和上限。 */
  static readonly MAX_BUDGET = 65536;

  private readonly sources = new Map<string, SourceState>();

  static limitsFor(sourceType: SourceType): SourceLimits {
    return { ...SOURCE_LIMITS[sourceType] };
  }

  /** 按来源类型截断触发数据：导航取低 3 位、事件取低 1 位。 */
  static truncateTriggerData(sourceType: SourceType, triggerData: number): number {
    const bits = SOURCE_LIMITS[sourceType].triggerDataBits;
    return triggerData & ((1 << bits) - 1);
  }

  private stateFor(sourceId: string): SourceState {
    let state = this.sources.get(sourceId);
    if (!state) {
      state = { sum: 0, reportCount: 0, dedupKeys: new Set() };
      this.sources.set(sourceId, state);
    }
    return state;
  }

  /**
   * 登记一条触发贡献。检查与提交在同一个同步区段内完成，
   * 浏览器宿主单线程事件循环下并发触发不会交错越过预算或重复计数。
   */
  register(trigger: Trigger): RegisterResult {
    const limits = SOURCE_LIMITS[trigger.sourceType];
    const triggerData = ContributionBudgetLedger.truncateTriggerData(
      trigger.sourceType,
      trigger.triggerData,
    );
    const state = this.stateFor(trigger.sourceId);
    const remaining = () => ContributionBudgetLedger.MAX_BUDGET - state.sum;
    const reject = (reason: RejectReason): RegisterResult => ({
      accepted: false,
      reason,
      triggerData,
      remainingBudget: remaining(),
      reportCount: state.reportCount,
    });

    if (
      !Number.isInteger(trigger.value) ||
      trigger.value < 0 ||
      !Number.isInteger(trigger.triggerData) ||
      trigger.triggerData < 0
    ) {
      return reject("invalid_trigger");
    }
    if (state.dedupKeys.has(trigger.dedupKey)) {
      return reject("duplicate_dedup_key");
    }
    if (state.reportCount >= limits.maxReports) {
      return reject("report_limit_reached");
    }
    // 超预算整条丢弃，不做部分计入。
    if (state.sum + trigger.value > ContributionBudgetLedger.MAX_BUDGET) {
      return reject("budget_exceeded");
    }

    state.sum += trigger.value;
    state.reportCount += 1;
    state.dedupKeys.add(trigger.dedupKey);
    return {
      accepted: true,
      triggerData,
      remainingBudget: remaining(),
      reportCount: state.reportCount,
    };
  }

  /** 查询某来源当前聚合贡献总和。 */
  contributionSum(sourceId: string): number {
    return this.sources.get(sourceId)?.sum ?? 0;
  }

  /** 查询某来源已用报告数。 */
  reportCount(sourceId: string): number {
    return this.sources.get(sourceId)?.reportCount ?? 0;
  }
}
