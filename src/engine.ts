import type {
  AuditRecord,
  Decision,
  EvaluationResult,
  Rule,
  RuleSet,
  Transaction,
  TxCondition,
  WindowCondition,
  WindowStats,
} from './types.ts';
import { WindowStore } from './windowStore.ts';
import { RuleRegistry } from './registry.ts';
import { AuditLog } from './auditLog.ts';

function matchTx(condition: TxCondition, tx: Transaction): boolean {
  if (condition.amountGt !== undefined && !(tx.amount > condition.amountGt)) return false;
  if (condition.amountGte !== undefined && !(tx.amount >= condition.amountGte)) return false;
  if (condition.amountLt !== undefined && !(tx.amount < condition.amountLt)) return false;
  if (condition.amountLte !== undefined && !(tx.amount <= condition.amountLte)) return false;
  if (condition.actionIn !== undefined && !condition.actionIn.includes(tx.action)) return false;
  if (condition.featureEquals !== undefined) {
    for (const [key, expected] of Object.entries(condition.featureEquals)) {
      if (tx.features?.[key] !== expected) return false;
    }
  }
  return true;
}

function matchWindow(condition: WindowCondition, stats: WindowStats): boolean {
  if (condition.countGt !== undefined && !(stats.count > condition.countGt)) return false;
  if (condition.countGte !== undefined && !(stats.count >= condition.countGte)) return false;
  if (condition.sumGt !== undefined && !(stats.sum > condition.sumGt)) return false;
  if (condition.sumGte !== undefined && !(stats.sum >= condition.sumGte)) return false;
  return true;
}

export class RiskEngine {
  readonly registry = new RuleRegistry();
  readonly windows = new WindowStore();
  readonly audit = new AuditLog();
  private sessionVersions = new Map<string, string>();

  publishRules(ruleSet: RuleSet): void {
    this.registry.publish(ruleSet);
  }

  beginSession(sessionId: string, at: number): string {
    const version = this.registry.activeAt(at).version;
    this.sessionVersions.set(sessionId, version);
    return version;
  }

  endSession(sessionId: string): void {
    this.sessionVersions.delete(sessionId);
  }

  ruleVersionFor(sessionId: string, at: number): string {
    const pinned = this.sessionVersions.get(sessionId);
    if (pinned !== undefined) return pinned;
    return this.registry.activeAt(at).version;
  }

  submit(tx: Transaction): EvaluationResult {
    if (!this.sessionVersions.has(tx.sessionId)) {
      this.beginSession(tx.sessionId, tx.timestamp);
    }
    const version = this.sessionVersions.get(tx.sessionId) as string;
    const pinnedSet = this.registry.get(version);

    const { decision, matchedRule, windowStats } = this.evaluate(tx, pinnedSet);

    this.windows.record({
      subjectId: tx.subjectId,
      action: tx.action,
      amount: tx.amount,
      timestamp: tx.timestamp,
    });

    const reason = matchedRule
      ? `matched rule ${matchedRule.id} (${matchedRule.name}) of rule set ${pinnedSet.version}`
      : `no rule matched under rule set ${pinnedSet.version}`;

    const audit: AuditRecord = this.audit.append({
      timestamp: tx.timestamp,
      transactionId: tx.id,
      sessionId: tx.sessionId,
      subjectId: tx.subjectId,
      action: tx.action,
      amount: tx.amount,
      decision,
      matchedRuleId: matchedRule ? matchedRule.id : null,
      ruleVersion: pinnedSet.version,
      windowStats,
      reason,
    });

    return { decision, matchedRuleId: audit.matchedRuleId, ruleVersion: audit.ruleVersion, audit };
  }

  private evaluate(
    tx: Transaction,
    ruleSet: RuleSet,
  ): { decision: Decision; matchedRule: Rule | null; windowStats: Record<string, WindowStats> } {
    const ordered = [...ruleSet.rules].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0));
    const windowStats: Record<string, WindowStats> = {};

    for (const rule of ordered) {
      if (rule.tx && !matchTx(rule.tx, tx)) continue;
      let allWindowsMatch = true;
      for (const wc of rule.windows ?? []) {
        const key = `${rule.id}:${wc.action}:${wc.windowMs}`;
        if (!(key in windowStats)) {
          windowStats[key] = this.windows.query(tx.subjectId, wc.action, tx.timestamp, wc.windowMs);
        }
        if (!matchWindow(wc, windowStats[key])) {
          allWindowsMatch = false;
          break;
        }
      }
      if (allWindowsMatch) {
        return { decision: rule.decision, matchedRule: rule, windowStats };
      }
    }
    return { decision: 'ALLOW', matchedRule: null, windowStats };
  }
}
