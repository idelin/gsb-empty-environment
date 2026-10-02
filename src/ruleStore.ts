import type {
  BitemporalRuleRecord,
  InteractionRule,
  LogicalTime,
} from "./types.ts";

export const FOREVER: LogicalTime = Number.POSITIVE_INFINITY;

/** 药品对归一化键（配伍禁忌为无序对） */
export function pairKey(drugA: string, drugB: string): string {
  return [drugA, drugB].sort().join("::");
}

/**
 * 双时态配伍禁忌规则库。
 *
 * 每次录入（含指南版本更新、规则更正）都会生成一条新的双时态记录，
 * 并将同一 ruleId 先前仍然"当前有效"的记录在知悉时间上封闭，
 * 从而完整保留"历史上何时知悉了什么"的版本链。
 */
export class BitemporalRuleStore {
  private records: BitemporalRuleRecord[] = [];

  /**
   * 录入一条规则。
   * @param rule       规则事实
   * @param validFrom  业务生效时间（指南生效日）
   * @param recordedAt 知悉时间（录入知识库的时间）
   * @param validTo    业务失效时间，默认永久有效
   */
  record(
    rule: InteractionRule,
    validFrom: LogicalTime,
    recordedAt: LogicalTime,
    validTo: LogicalTime = FOREVER,
  ): BitemporalRuleRecord {
    for (const existing of this.records) {
      if (existing.ruleId === rule.ruleId && existing.recordedUntil === FOREVER) {
        existing.recordedUntil = recordedAt;
      }
    }
    const entry: BitemporalRuleRecord = {
      ...rule,
      validFrom,
      validTo,
      recordedAt,
      recordedUntil: FOREVER,
    };
    this.records.push(entry);
    return entry;
  }

  /**
   * 双时态查询：在"知悉时间不晚于 knownAt"的认知下，
   * 返回业务时间 validTime 时临床上生效的全部规则记录。
   */
  rulesEffectiveAt(
    validTime: LogicalTime,
    knownAt: LogicalTime,
  ): BitemporalRuleRecord[] {
    return this.records.filter(
      (r) =>
        r.validFrom <= validTime &&
        validTime < r.validTo &&
        r.recordedAt <= knownAt &&
        knownAt < r.recordedUntil,
    );
  }

  /** 全部原始记录（用于审计/调试） */
  allRecords(): readonly BitemporalRuleRecord[] {
    return this.records;
  }
}
