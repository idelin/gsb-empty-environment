/**
 * Core domain model for the rule-set comparison tool.
 *
 * Evaluation strategy (by design, enforced in eval.ts):
 * - rules read ONLY explicit attributes present on the input object;
 * - the attribute universe is declared up front as a finite domain
 *   (finite sets + bounded integer ranges), so no external identity
 *   service, remote lookup or environment access is ever performed;
 * - conditions are limited to finite-set membership and integer
 *   comparisons combined with boolean AND/OR/NOT.
 */

export type Primitive = string | number | boolean;

/** A concrete request / input point: explicit attributes only. */
export type Input = Readonly<Record<string, Primitive>>;

export type Effect = "allow" | "deny";

/** Finite set membership test over an explicit attribute. */
export interface InCondition {
  kind: "in";
  attr: string;
  values: ReadonlyArray<Primitive>;
}

/** Integer comparison test over an explicit attribute. */
export interface IntCondition {
  kind: "int";
  attr: string;
  op: "=" | "!=" | "<" | "<=" | ">" | ">=";
  value: number;
}

export type Condition =
  | InCondition
  | IntCondition
  | { kind: "and"; of: ReadonlyArray<Condition> }
  | { kind: "or"; of: ReadonlyArray<Condition> }
  | { kind: "not"; of: Condition };

export interface Rule {
  /** Stable identifier used only in the explanation of a decision. */
  id: string;
  effect: Effect;
  when: Condition;
}

export interface RuleSet {
  name: string;
  rules: ReadonlyArray<Rule>;
  /**
   * Effect returned when no rule matches. Order-independent semantics:
   * every matching allow/deny rule is collected, deny overrides allow.
   * Reordering or commuting rules therefore cannot change the effect.
   */
  defaultEffect: Effect;
}

/** Explanation of a decision: the rules that actually fired. */
export interface Decision {
  effect: Effect;
  /** Sorted rule ids whose condition matched the input. */
  matchedRuleIds: ReadonlyArray<string>;
  /** True when the effect came from defaultEffect (no rule matched). */
  defaulted: boolean;
}

/** Declared universe of analysis, all attributes are explicit and finite. */
export type DomainSpec = Readonly<Record<string, AttributeDomain>>;

export interface SetDomain {
  kind: "set";
  values: ReadonlyArray<Primitive>;
}

export interface IntRangeDomain {
  kind: "intRange";
  min: number;
  max: number;
}

export type AttributeDomain = SetDomain | IntRangeDomain;
