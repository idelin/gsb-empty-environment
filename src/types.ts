export type Day = number;
export type KpiId = string;

/** 单条 KPI 时态参数：目标值与权重在 [validFrom, validTo) 内有效 */
export interface KpiParamEntry {
  kpiId: KpiId;
  target: number;
  weight: number;
  validFrom: Day; // 含
  validTo: Day;   // 不含
}

/** 提交新版本时的输入 */
export interface VersionInput {
  author: string;        // 变更责任人
  recordedAtDay: Day;    // 版本登记日（可晚于生效日，即追溯调整）
  reason: string;        // 变更原因（战略说明）
  entries: KpiParamEntry[];
}

/** 带责任人签名的不可变版本 */
export interface Version extends VersionInput {
  id: number;
  signature: string;     // HMAC-SHA256(责任人密钥, 规范化版本内容)
}

export interface KpiContribution {
  kpiId: KpiId;
  weight: number;
  target: number;
  actual: number;
  rate: number;          // actual / target
  weighted: number;      // weight * rate
}

export interface SegmentScore {
  from: Day;
  to: Day;
  days: number;
  score: number;
  contributions: KpiContribution[];
}

export interface ScoreReport {
  versionId: number;
  periodFrom: Day;
  periodTo: Day;
  totalScore: number;    // 按天加权的季度末最终得分
  segments: SegmentScore[];
}

/** 版本影响：新版本相对上一版本的得分差 */
export interface VersionImpact {
  versionId: number;
  author: string;
  recordedAtDay: Day;
  reason: string;
  previousVersionId: number | null;
  previousScore: number | null;
  newScore: number | null;
  delta: number | null;
}
