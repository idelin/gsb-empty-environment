export type Decision = 'allow' | 'deny';

export type Tags = Record<string, string>;

export type Effect = 'Allow' | 'Deny';

export type TriState = true | false | null;

export type StringOperator =
  | 'StringEquals'
  | 'StringNotEquals'
  | 'StringLike'
  | 'StringNotLike';

export type NumericOperator =
  | 'NumericEquals'
  | 'NumericNotEquals'
  | 'NumericLessThan'
  | 'NumericLessThanEquals'
  | 'NumericGreaterThan'
  | 'NumericGreaterThanEquals';

export type BoolOperator = 'Bool';

export type ConditionOperator = StringOperator | NumericOperator | BoolOperator;

export type Quantifier = 'ForAnyValue' | 'ForAllValues';

export interface LeafCondition {
  kind: 'leaf';
  operator: ConditionOperator;
  key: string;
  values: (string | number | boolean)[];
  quantifier?: Quantifier;
}

export interface GroupCondition {
  kind: 'and' | 'or' | 'not';
  children: ConditionNode[];
}

export type ConditionNode = LeafCondition | GroupCondition;

export interface Statement {
  sid?: string;
  effect: Effect;
  actions: string[];
  resources: string[];
  principals?: string[];
  condition?: ConditionNode;
}

export interface Policy {
  id: string;
  account?: string;
  statements: Statement[];
}

export interface ResourceNode {
  id: string;
  parent?: string;
  tags?: Tags;
  policies?: Policy[];
}

export interface Principal {
  id: string;
  type: 'user' | 'role' | 'service';
  account: string;
  tags?: Tags;
}

export interface EvalContext {
  principal: Principal;
  action: string;
  resourceId: string;
  resourceTags: Tags;
  requestTags?: Tags;
  extra?: Record<string, string | number | boolean | string[]>;
}

export type DenyReason =
  | 'explicit-deny'
  | 'condition-key-missing'
  | 'no-matching-statement'
  | 'trust-account-mismatch'
  | 'trust-principal-mismatch';

export interface EvaluationResult {
  decision: Decision;
  reason: DenyReason | 'allowed';
  matchedPolicyId?: string;
  matchedStatementSid?: string;
  deniedPolicyId?: string;
  deniedStatementSid?: string;
  missingKeys?: string[];
  trace: TraceEntry[];
}

export interface TraceEntry {
  policyId: string;
  statementSid?: string;
  effect: Effect;
  status: 'match' | 'action-or-resource-mismatch' | 'condition-false' | 'condition-missing';
  missingKeys?: string[];
}

export interface TrustEvaluationResult {
  decision: Decision;
  reason: DenyReason | 'allowed';
  roleAccount: string;
  principalId: string;
  missingKeys?: string[];
}

export interface AccessRequest {
  principal: Principal;
  action: string;
  resourceId: string;
  requestTags?: Tags;
  identityPolicies?: Policy[];
  extra?: Record<string, string | number | boolean | string[]>;
}
