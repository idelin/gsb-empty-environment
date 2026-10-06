export type FlagValue = string | number | boolean;
export type AttributeValue = string | number | boolean;

/**
 * A user the flags are evaluated for.
 *
 * `attributesVersion` must be bumped whenever `attributes` change; it is part
 * of the evaluation cache key, so a stale version means stale cached results.
 */
export interface UserContext {
  id: string;
  attributes: Record<string, AttributeValue>;
  attributesVersion: number;
}

export type ConditionOp =
  | "eq"
  | "neq"
  | "in"
  | "gt"
  | "gte"
  | "lt"
  | "lte"
  | "contains"
  | "exists"
  | "notExists";

export interface Condition {
  attr: string;
  op: ConditionOp;
  value?: AttributeValue | AttributeValue[];
}

/**
 * Percentage gate expressed in basis points (0..10000) so it maps 1:1 onto
 * the mod-10000 bucket space: a user is included iff bucket < basisPoints.
 */
export interface Rollout {
  basisPoints: number;
  salt?: string;
}

export interface GroupRule {
  id: string;
  /** Higher priority wins; ties are broken by ascending rule id. */
  priority: number;
  conditions: Condition[];
  value: FlagValue;
  rollout?: Rollout;
}

export interface FlagDependency {
  flagId: string;
  equals: FlagValue;
}

export interface FlagConfig {
  id: string;
  defaultValue: FlagValue;
  /** Served when a dependency is unmet or the rollout gate excludes the user. Defaults to false. */
  fallbackValue?: FlagValue;
  /** Explicit per-user values; win over every other rule. */
  overrides?: Record<string, FlagValue>;
  groups?: GroupRule[];
  /** Gate around `defaultValue`. */
  rollout?: Rollout;
  dependsOn?: FlagDependency[];
}

/** An immutable, versioned view of the whole flag configuration. */
export interface ConfigSnapshot {
  version: number;
  flags: Readonly<Record<string, FlagConfig>>;
  createdAtMs: number;
}

export type EvaluationReason =
  | "override"
  | "group"
  | "default"
  | "fallback"
  | "dependency-unmet";

export interface Evaluation {
  flagId: string;
  value: FlagValue;
  reason: EvaluationReason;
  snapshotVersion: number;
  groupId?: string;
  bucket?: number;
}

export interface EvaluateOptions {
  /** Snapshot version to evaluate against; defaults to the current one. */
  version?: number;
  /** Defaults to true. Pass false for a full (uncached) evaluation. */
  useCache?: boolean;
}
