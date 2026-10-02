export type Decision = 'ALLOW' | 'REVIEW' | 'BLOCK';

export interface Transaction {
  id: string;
  sessionId: string;
  subjectId: string;
  action: string;
  amount: number;
  timestamp: number;
  currency?: string;
  features?: Record<string, number | string | boolean>;
}

export interface TxCondition {
  amountGt?: number;
  amountGte?: number;
  amountLt?: number;
  amountLte?: number;
  actionIn?: string[];
  featureEquals?: Record<string, number | string | boolean>;
}

export interface WindowCondition {
  action: string;
  windowMs: number;
  countGt?: number;
  countGte?: number;
  sumGt?: number;
  sumGte?: number;
}

export interface Rule {
  id: string;
  name: string;
  decision: Decision;
  priority?: number;
  tx?: TxCondition;
  windows?: WindowCondition[];
}

export interface RuleSet {
  version: string;
  effectiveFrom: number;
  rules: Rule[];
}

export interface WindowStats {
  count: number;
  sum: number;
}

export interface AuditRecord {
  seq: number;
  timestamp: number;
  transactionId: string;
  sessionId: string;
  subjectId: string;
  action: string;
  amount: number;
  decision: Decision;
  matchedRuleId: string | null;
  ruleVersion: string;
  windowStats: Record<string, WindowStats>;
  reason: string;
}

export interface EvaluationResult {
  decision: Decision;
  matchedRuleId: string | null;
  ruleVersion: string;
  audit: AuditRecord;
}
