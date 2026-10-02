import type { BitemporalRuleRecord, DrugPairRule } from './types.ts';

export interface RecordRuleOptions {
  /** 知悉时间：规则被系统收录的时间（必填） */
  readonly recordedAt: number;
  /** 业务时间起点：规则在临床上生效的时间，默认 0（自始生效） */
  readonly validFrom?: number;
  /** 业务时间终点：规则在临床上失效的时间，默认 Infinity（至今有效） */
  readonly validTo?: number;
}

/** 可变的内部记录（recordedTo 会在版本更替时被关闭） */
type MutableRecord = {
  -readonly [K in keyof BitemporalRuleRecord]: BitemporalRuleRecord[K];
};

/**
 * 双时态配伍禁忌规则库。
 * 规则随临床指南版本演进：同一 ruleId 重新收录时，旧知悉版本被关闭
 * （recordedTo = 新版本的 recordedAt），历史知悉状态仍可回溯查询。
 */
export class BitemporalRuleStore {
  private readonly records: MutableRecord[] = [];

  /** 收录（或更替）一条规则，返回新记录 */
  recordRule(rule: DrugPairRule, options: RecordRuleOptions): BitemporalRuleRecord {
    const { recordedAt } = options;
    for (const existing of this.records) {
      if (existing.ruleId === rule.ruleId && existing.recordedTo === Infinity) {
        if (recordedAt < existing.recordedFrom) {
          throw new Error(
            `知悉时间回退：规则 ${rule.ruleId} 当前知悉区间起于 ${existing.recordedFrom}，无法在 ${recordedAt} 收录`,
          );
        }
        existing.recordedTo = recordedAt;
      }
    }
    const record: MutableRecord = {
      ...rule,
      validFrom: options.validFrom ?? 0,
      validTo: options.validTo ?? Infinity,
      recordedFrom: recordedAt,
      recordedTo: Infinity,
    };
    this.records.push(record);
    return { ...record };
  }

  /** 撤销一条规则：关闭其当前知悉版本，不再收录新版本 */
  retractRule(ruleId: string, recordedAt: number): void {
    const open = this.records.filter(
      (r) => r.ruleId === ruleId && r.recordedTo === Infinity,
    );
    if (open.length === 0) {
      throw new Error(`规则 ${ruleId} 不存在或已被撤销`);
    }
    for (const record of open) {
      if (recordedAt < record.recordedFrom) {
        throw new Error(`知悉时间回退：无法在 ${recordedAt} 撤销规则 ${ruleId}`);
      }
      record.recordedTo = recordedAt;
    }
  }

  /**
   * 双时态查询：在指定知悉时间与业务时间下生效的规则。
   * recordedFrom <= knowledgeTime < recordedTo 且 validFrom <= businessTime < validTo
   */
  rulesInEffect(knowledgeTime: number, businessTime: number): BitemporalRuleRecord[] {
    return this.records
      .filter(
        (r) =>
          r.recordedFrom <= knowledgeTime &&
          knowledgeTime < r.recordedTo &&
          r.validFrom <= businessTime &&
          businessTime < r.validTo,
      )
      .map((r) => ({ ...r }));
  }

  /** 规则库当前最新知悉时间（所有知悉事件的最大值），空库返回 0 */
  latestKnowledgeTime(): number {
    let latest = 0;
    for (const r of this.records) {
      latest = Math.max(latest, r.recordedFrom);
      if (r.recordedTo !== Infinity) latest = Math.max(latest, r.recordedTo);
    }
    return latest;
  }
}
