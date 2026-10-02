/**
 * 双时态模型：
 * - 业务时间 (valid time)：规则在临床上生效的时间（指南生效日期）。
 * - 知悉时间 (transaction time)：规则被录入知识库、系统开始知悉该规则的时间。
 *
 * 时间统一使用逻辑时间戳（number），可映射到任意真实时钟。
 */

export type LogicalTime = number;

export type DrugId = string;

/** 禁忌严重程度 */
export const Severity = {
  /** 禁用：绝对禁忌 */
  CONTRAINDICATED: "contraindicated",
  /** 慎用：需谨慎联用 */
  CAUTION: "caution",
} as const;

export type Severity = (typeof Severity)[keyof typeof Severity];

/** 一条配伍禁忌规则（不可变事实，由规则库赋予双时态区间） */
export interface InteractionRule {
  /** 规则标识：同一 id 的多次录入构成该规则的版本链 */
  ruleId: string;
  drugA: DrugId;
  drugB: DrugId;
  severity: Severity;
  /** 来源指南版本，用于审计追溯 */
  guidelineVersion: string;
  /** 临床说明 */
  note?: string;
}

/** 规则库内部存储的双时态记录 */
export interface BitemporalRuleRecord extends InteractionRule {
  /** 业务时间区间 [validFrom, validTo) */
  validFrom: LogicalTime;
  validTo: LogicalTime;
  /** 知悉时间区间 [recordedAt, recordedUntil) */
  recordedAt: LogicalTime;
  recordedUntil: LogicalTime;
}

export interface Prescription {
  prescriptionId: string;
  /** 开立的业务时间 */
  issuedAt: LogicalTime;
  drugs: DrugId[];
}

/** 命中的一条规则（附判定所用双时态坐标） */
export interface RuleHit {
  rule: BitemporalRuleRecord;
  drugA: DrugId;
  drugB: DrugId;
}

export interface ComplianceReport {
  prescriptionId: string;
  /** 判定坐标：以处方开立时间同时作为业务时间与知悉时间截点 */
  evaluatedAt: { validTime: LogicalTime; knownAt: LogicalTime };
  compliant: boolean;
  violations: RuleHit[];
}

export interface RiskFinding extends RuleHit {
  /** 该规则被知悉的时间（晚于处方开立时间即为追溯性风险） */
  ruleKnownAt: LogicalTime;
  guidelineVersion: string;
}

export interface RiskAuditReport {
  prescriptionId: string;
  /** 审计坐标：业务时间与知悉时间均取审计时刻（最新规则回看历史处方） */
  evaluatedAt: { validTime: LogicalTime; knownAt: LogicalTime };
  hasRetrospectiveRisk: boolean;
  findings: RiskFinding[];
}
