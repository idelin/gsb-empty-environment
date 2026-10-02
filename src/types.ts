/** 禁忌严重程度：contraindicated=禁用（不合规），caution=慎用（合规但需警示） */
export type Severity = 'contraindicated' | 'caution';

/** 药品配伍禁忌规则（某一临床指南版本下的静态内容） */
export interface DrugPairRule {
  readonly ruleId: string;
  readonly drugA: string;
  readonly drugB: string;
  readonly severity: Severity;
  readonly guidelineVersion: string;
  readonly description: string;
}

/**
 * 双时态规则记录：
 * - 业务时间（valid time）：规则在临床上生效的时间区间 [validFrom, validTo)
 * - 知悉时间（transaction time）：规则被系统收录/知悉的时间区间 [recordedFrom, recordedTo)
 */
export interface BitemporalRuleRecord extends DrugPairRule {
  readonly validFrom: number;
  readonly validTo: number;
  readonly recordedFrom: number;
  readonly recordedTo: number;
}

/** 历史处方 */
export interface Prescription {
  readonly id: string;
  /** 业务时间：处方开立时间 */
  readonly issuedAt: number;
  readonly drugs: readonly string[];
}

/** 一次配伍命中 */
export interface RuleHit {
  readonly rule: DrugPairRule;
  readonly drugA: string;
  readonly drugB: string;
}

/** 开方合规性报告（以指定知悉时间回看开方时刻） */
export interface ComplianceReport {
  readonly prescriptionId: string;
  readonly issuedAt: number;
  readonly asOfKnowledge: number;
  readonly compliant: boolean;
  /** 禁用级命中（导致不合规） */
  readonly violations: readonly RuleHit[];
  /** 慎用级命中（合规但需警示） */
  readonly warnings: readonly RuleHit[];
}

/** 追溯风险条目：开方时未知悉、如今规则下存在的风险 */
export interface RetroactiveRisk {
  readonly drugA: string;
  readonly drugB: string;
  readonly severity: Severity;
  readonly guidelineVersion: string;
  /** 该规则被系统知悉的时间 */
  readonly knownSince: number;
  readonly description: string;
}

/** 风险审计报告：以最新知悉回看历史处方 */
export interface RiskAuditReport {
  readonly prescriptionId: string;
  readonly issuedAt: number;
  readonly auditedAtKnowledge: number;
  /** 开方当时（按当时知悉的规则）是否合规 */
  readonly compliantAtIssue: boolean;
  /** 是否存在追溯风险 */
  readonly hasRetroactiveRisk: boolean;
  readonly retroactiveRisks: readonly RetroactiveRisk[];
}
