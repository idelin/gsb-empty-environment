import type { AuditEntry, Decision } from './types.ts';

/** 追加式审计日志：每次裁决都记录实际命中的规则版本号 */
export class AuditLog {
  private entries: AuditEntry[] = [];

  record(decision: Decision): AuditEntry {
    const entry: AuditEntry = {
      seq: this.entries.length + 1,
      timestamp: decision.timestamp,
      txId: decision.txId,
      sessionId: decision.sessionId,
      ruleVersion: decision.ruleVersion,
      action: decision.action,
      matchedRuleIds: [...decision.matchedRuleIds],
      reason: decision.reason,
    };
    this.entries.push(entry);
    return entry;
  }

  all(): readonly AuditEntry[] {
    return this.entries;
  }

  findByTx(txId: string): AuditEntry | undefined {
    return this.entries.find((e) => e.txId === txId);
  }
}
