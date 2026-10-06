export type FlagValue = string | number | boolean;

export type ConditionOp = 'eq' | 'neq' | 'in';

export interface AttributeCondition {
  key: string;
  op: ConditionOp;
  /** string for eq/neq, string[] for in. */
  value: string | string[];
}

export interface Rollout {
  /** 0..100, may be fractional. Bucket b passes iff b < percentage * 100. */
  percentage: number;
  salt?: string;
}

export interface GroupRule {
  id: string;
  /** Higher priority wins; ties broken by ascending id. */
  priority: number;
  conditions: AttributeCondition[];
  value: FlagValue;
  /** Omitted means 100%. */
  rollout?: Rollout;
}

export interface FlagDependency {
  flagId: string;
  requiredValue: FlagValue;
}

export interface FlagConfig {
  id: string;
  defaultValue: FlagValue;
  /** Value when a dependency is unmet; defaults to defaultValue. */
  fallbackValue?: FlagValue;
  /** Explicit per-user overrides; highest precedence. */
  overrides?: Record<string, FlagValue>;
  groups?: GroupRule[];
  dependencies?: FlagDependency[];
}

export interface UserContext {
  id: string;
  attributes?: Record<string, string>;
  /** Bumped by the caller whenever attributes change; part of the cache key. */
  attrVersion?: number;
}

export interface DependencyCheck {
  flagId: string;
  requiredValue: FlagValue;
  actualValue: FlagValue;
  met: boolean;
}

export type EvalReason = 'override' | 'group' | 'default' | 'dependency_unmet';

export interface EvalResult {
  flagId: string;
  value: FlagValue;
  reason: EvalReason;
  matchedGroupId?: string;
  bucket?: number;
  bucketKey?: string;
  dependencies: DependencyCheck[];
  snapshotVersion: number;
}
