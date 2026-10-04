/**
 * Core domain types for the backup rule interpreter.
 *
 * A policy is an ordered set of rules. Each rule carries a boolean condition
 * over file metadata (path, size, modification version, tags) and an action.
 * Rules are selected by priority: the highest-priority rule whose condition
 * is definitely true wins. Conditions evaluate to a tri-state so that missing
 * metadata (e.g. absent tags) yields "unknown" instead of silently excluding
 * the file.
 */

export type Tri = 'true' | 'false' | 'unknown';

export type Action = 'include' | 'exclude';

export interface FileMeta {
  path: string;
  /** Bytes. Undefined means the size is currently unknown. */
  size?: number;
  /** Monotonic modification version. Undefined means unknown. */
  version?: number;
  /** Tag set. Undefined means tags have not been fetched yet. */
  tags?: readonly string[];
}

export type Condition =
  | { kind: 'path'; glob: string }
  | { kind: 'size'; min?: number; max?: number } // inclusive bounds
  | { kind: 'version'; min?: number; max?: number } // inclusive bounds
  | { kind: 'tag'; tag: string; negate?: boolean }
  | { kind: 'not'; cond: Condition }
  | { kind: 'all'; conds: readonly Condition[] }
  | { kind: 'any'; conds: readonly Condition[] };

export interface Rule {
  id: string;
  /** Higher priority wins. Ties are broken by id for determinism. */
  priority: number;
  action: Action;
  when: Condition;
}

export interface Policy {
  name: string;
  /** Applied only when every rule evaluates to a definite "false". */
  defaultAction: Action;
  rules: readonly Rule[];
}

/** The outcome of interpreting one file against a policy. */
export type Decision =
  | { kind: 'decided'; action: Action; ruleId: string }
  | { kind: 'default'; action: Action }
  | { kind: 'unknown'; pendingRuleIds: string[] };

export type EffectiveAction = Action | 'unknown';

export function effectiveAction(decision: Decision): EffectiveAction {
  return decision.kind === 'unknown' ? 'unknown' : decision.action;
}

export function decisionEquals(a: Decision, b: Decision): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === 'decided' && b.kind === 'decided') {
    return a.action === b.action && a.ruleId === b.ruleId;
  }
  if (a.kind === 'default' && b.kind === 'default') {
    return a.action === b.action;
  }
  if (a.kind === 'unknown' && b.kind === 'unknown') {
    return (
      a.pendingRuleIds.length === b.pendingRuleIds.length &&
      a.pendingRuleIds.every((id, i) => id === b.pendingRuleIds[i])
    );
  }
  return false;
}
