export type Effect = 'allow' | 'deny';

export type SubjectKind = 'user' | 'group';

export interface Rule {
  /** Unique rule identifier. */
  id: string;
  /** User id or group id the rule applies to. */
  subject: string;
  subjectKind: SubjectKind;
  /** Path prefix the rule applies to, e.g. "/repos/backend". */
  prefix: string;
  effect: Effect;
}

export interface GroupDef {
  /** Direct user members. */
  members?: string[];
  /**
   * Included groups: members of each included group are also treated as
   * members of this group (inheritance). Cycles are rejected at update time.
   */
  groups?: string[];
}

export interface PolicyConfig {
  groups: Record<string, GroupDef>;
  rules: Rule[];
}

export interface Decision {
  user: string;
  path: string;
  /** Final effect. "deny" with defaultDeny=true means no rule matched. */
  effect: Effect;
  /** True when no rule matched and the default-deny fallback decided. */
  defaultDeny: boolean;
  /** The rule that decided the outcome, or null on default deny. */
  effective: Rule | null;
  /**
   * Rules that matched but did not decide: less specific rules, same-depth
   * allows beaten by a deny, and same-depth ties lost on id order.
   */
  shadowed: Rule[];
  /** All rules that matched the (user, path) pair, before resolution. */
  matched: Rule[];
}

export type ValidationErrorKind =
  | 'duplicate-rule-id'
  | 'invalid-prefix'
  | 'invalid-effect'
  | 'unknown-group'
  | 'inheritance-cycle';

export interface ValidationError {
  kind: ValidationErrorKind;
  message: string;
  /** Populated for inheritance-cycle errors: the groups forming the cycle. */
  cycle?: string[];
}

export type UpdateResult = { ok: true } | { ok: false; errors: ValidationError[] };
