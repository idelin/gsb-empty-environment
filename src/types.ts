/** 交易类型 */
export type TxType = 'transfer' | 'payment' | 'withdrawal';

/** 风控动作，严重级别 block > review > allow */
export type RiskAction = 'allow' | 'review' | 'block';

/** 一笔待裁决的交易（流式输入事件） */
export interface Transaction {
  id: string;
  accountId: string;
  type: TxType;
  amount: number;
  currency: string;
  /** 逻辑时间戳（秒），由上游事件携带，保证测试可确定性重放 */
  timestamp: number;
  /** 长事务/会话标识；携带该字段的请求按会话锁定的规则版本裁定 */
  sessionId?: string;
}

/** 滑动窗口聚合条件：窗口内历史频次 / 累计金额 */
export interface WindowCondition {
  /** 窗口长度（秒），如 10 分钟 = 600 */
  windowSeconds: number;
  /** 参与聚合的交易类型，缺省为全部类型 */
  txTypes?: TxType[];
  /** 窗口内交易笔数 > 该值（含当前这笔） */
  countGt?: number;
  /** 窗口内累计金额 > 该值（含当前这笔） */
  sumAmountGt?: number;
}

/** 规则条件：单笔特征 + 可选的滑动窗口历史聚合，二者为 AND 关系 */
export interface RuleCondition {
  txTypes?: TxType[];
  /** 单笔金额 > 该值 */
  amountGt?: number;
  currency?: string;
  window?: WindowCondition;
}

export interface Rule {
  id: string;
  description: string;
  condition: RuleCondition;
  action: RiskAction;
}

/** 一个不可变的规则版本 */
export interface RuleSet {
  version: string;
  rules: Rule[];
}

/** 裁决结果 */
export interface Decision {
  txId: string;
  sessionId?: string;
  /** 实际用于裁决的规则版本号（审计关键字段） */
  ruleVersion: string;
  action: RiskAction;
  matchedRuleIds: string[];
  timestamp: number;
  reason: string;
}

/** 审计日志条目 */
export interface AuditEntry {
  seq: number;
  timestamp: number;
  txId: string;
  sessionId?: string;
  ruleVersion: string;
  action: RiskAction;
  matchedRuleIds: string[];
  reason: string;
}
