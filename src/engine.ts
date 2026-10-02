import { AuditLog } from './audit.ts';
import type {
  Decision,
  RiskAction,
  Rule,
  RuleSet,
  Transaction,
  TxType,
} from './types.ts';

const SEVERITY: Record<RiskAction, number> = { allow: 0, review: 1, block: 2 };

/**
 * 流式风控规则引擎。
 *
 * - 规则条件 = 单笔交易特征 AND 滑动窗口历史聚合（频次 / 累计金额）。
 * - 规则库支持热切换：updateRules 之后，新请求立即使用新版本；
 *   已通过 beginSession 锁定的长事务会话仍按旧版本裁定，实现平滑过渡。
 * - 每次裁决写入审计日志，携带实际使用的规则版本号。
 */
export class RiskEngine {
  private activeRuleSet: RuleSet;
  /** sessionId -> 会话开始时锁定的规则版本 */
  private pinnedSessions = new Map<string, RuleSet>();
  /** accountId -> 该账户的历史交易（按时间升序），用于滑动窗口聚合 */
  private history = new Map<string, Transaction[]>();
  readonly audit = new AuditLog();

  constructor(ruleSet: RuleSet) {
    this.activeRuleSet = ruleSet;
  }

  /** 当前对新请求生效的规则版本 */
  get activeVersion(): string {
    return this.activeRuleSet.version;
  }

  /** 热切换规则库：仅影响未绑定会话的新请求，已锁定会话平稳过渡 */
  updateRules(ruleSet: RuleSet): void {
    this.activeRuleSet = ruleSet;
  }

  /** 长事务开始：锁定当前规则版本，会话全程按该版本裁定 */
  beginSession(sessionId: string): void {
    if (!this.pinnedSessions.has(sessionId)) {
      this.pinnedSessions.set(sessionId, this.activeRuleSet);
    }
  }

  /** 长事务结束：释放版本锁定，后续请求回到当前生效版本 */
  endSession(sessionId: string): void {
    this.pinnedSessions.delete(sessionId);
  }

  /** 会话锁定的规则版本（未锁定时返回 undefined） */
  pinnedVersion(sessionId: string): string | undefined {
    return this.pinnedSessions.get(sessionId)?.version;
  }

  /** 裁决一笔交易并写入审计日志 */
  evaluate(tx: Transaction): Decision {
    const ruleSet =
      (tx.sessionId && this.pinnedSessions.get(tx.sessionId)) ??
      this.activeRuleSet;

    const matched: Rule[] = ruleSet.rules.filter((rule) =>
      this.matches(rule, tx),
    );

    let action: RiskAction = 'allow';
    for (const rule of matched) {
      if (SEVERITY[rule.action] > SEVERITY[action]) action = rule.action;
    }

    const decision: Decision = {
      txId: tx.id,
      sessionId: tx.sessionId,
      ruleVersion: ruleSet.version,
      action,
      matchedRuleIds: matched.map((r) => r.id),
      timestamp: tx.timestamp,
      reason:
        matched.length === 0
          ? 'no rule matched'
          : matched.map((r) => `${r.id}: ${r.description}`).join('; '),
    };

    this.appendHistory(tx);
    this.audit.record(decision);
    return decision;
  }

  private matches(rule: Rule, tx: Transaction): boolean {
    const cond = rule.condition;
    if (cond.txTypes && !cond.txTypes.includes(tx.type)) return false;
    if (cond.amountGt !== undefined && tx.amount <= cond.amountGt) return false;
    if (cond.currency !== undefined && tx.currency !== cond.currency) {
      return false;
    }
    if (cond.window) {
      const win = cond.window;
      const from = tx.timestamp - win.windowSeconds;
      const inWindow = this.windowOf(tx.accountId, from, tx.timestamp, win.txTypes);
      // 窗口聚合包含当前这笔交易
      const count = inWindow.length + 1;
      const sum = inWindow.reduce((acc, t) => acc + t.amount, 0) + tx.amount;
      if (win.countGt !== undefined && count <= win.countGt) return false;
      if (win.sumAmountGt !== undefined && sum <= win.sumAmountGt) return false;
    }
    return true;
  }

  private windowOf(
    accountId: string,
    fromExclusive: number,
    toInclusive: number,
    txTypes?: TxType[],
  ): Transaction[] {
    const list = this.history.get(accountId) ?? [];
    return list.filter(
      (t) =>
        t.timestamp > fromExclusive &&
        t.timestamp <= toInclusive &&
        (!txTypes || txTypes.includes(t.type)),
    );
  }

  private appendHistory(tx: Transaction): void {
    let list = this.history.get(tx.accountId);
    if (!list) {
      list = [];
      this.history.set(tx.accountId, list);
    }
    list.push(tx);
    // 只保留评估所需的最长窗口之外无需复杂处理：按时间升序追加即可，
    // 查询时按窗口过滤；为防止无限增长，裁剪掉早于“最新时间 - 1 小时”的记录。
    const cutoff = tx.timestamp - 3600;
    let drop = 0;
    while (drop < list.length && list[drop].timestamp <= cutoff) drop++;
    if (drop > 0) list.splice(0, drop);
  }
}
