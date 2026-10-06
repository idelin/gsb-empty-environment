export type Effect = 'allow' | 'deny';

export interface Rule {
  prefix: string;
  effect: Effect;
}

export interface GroupConfig {
  rules?: Rule[];
  inherits?: string[];
}

export interface PolicyConfig {
  groups: Record<string, GroupConfig>;
}

/** A rule after expansion: knows which group declared it. */
export interface RuleRef {
  /** Group that declared the rule. */
  origin: string;
  /** Index of the rule within the declaring group's `rules` array. */
  index: number;
  /** Normalized rule (prefix normalized, effect validated). */
  rule: Rule;
  /** Segment depth of the normalized prefix. */
  depth: number;
  /** Expansion rank: own rules of the queried group first, then inherited
   *  rules in depth-first `inherits` order. Lower rank wins duplicates. */
  rank: number;
}

export interface Decision {
  group: string;
  /** Normalized request path. */
  path: string;
  effect: Effect;
  /** The rule that determined the effect, or null on default-deny. */
  winner: RuleRef | null;
  /** All effective rules matching the path, most specific first. */
  matched: RuleRef[];
  reason: 'rule' | 'default-deny' | 'unknown-group';
}

export interface ShadowedEntry {
  shadowed: RuleRef;
  shadowedBy: RuleRef;
  reason: 'deny-wins-same-prefix' | 'duplicate';
}

export interface GroupExplanation {
  group: string;
  /** Rules actually in effect for the group (shadowed ones excluded). */
  effective: RuleRef[];
  /** Declared/expanded rules that can never win a decision. */
  shadowed: ShadowedEntry[];
}

export type UpdateError =
  | { kind: 'inheritance-cycle'; cycle: string[] }
  | { kind: 'unknown-group'; group: string; referencedBy: string }
  | { kind: 'invalid-config'; message: string };

export type UpdateResult = { ok: true } | { ok: false; error: UpdateError };
