/**
 * Shared model for separation-of-duties (SoD) assignment checking.
 *
 * One `solve` call checks whether a set of positions can be staffed by
 * distinct canonical subjects, honouring qualification policies, subject
 * aliases, explicit exceptions and an exception budget.
 */

/** Attribute values are explicit data only: integers, strings, booleans, or finite string/number lists. */
export type AttributeValue = number | string | boolean | readonly (number | string)[];
export type Attributes = Record<string, AttributeValue>;

/**
 * Policy language (deliberately closed world):
 * - reads only explicit subject attributes, never external identity services;
 * - finite-set membership (`in` / `notIn`);
 * - integer conditions (`lt` / `lte` / `gt` / `gte`, plus `eq` / `neq`);
 * - boolean combinations (`and` / `or` / `not`).
 */
export type PolicyExpr =
  | { readonly op: 'has'; readonly attr: string }
  | { readonly op: 'eq' | 'neq'; readonly attr: string; readonly value: number | string | boolean }
  | { readonly op: 'lt' | 'lte' | 'gt' | 'gte'; readonly attr: string; readonly value: number }
  | { readonly op: 'in' | 'notIn'; readonly attr: string; readonly set: readonly (number | string)[] }
  | { readonly op: 'and' | 'or'; readonly args: readonly PolicyExpr[] }
  | { readonly op: 'not'; readonly arg: PolicyExpr };

export interface Subject {
  /** Canonical subject id. Also a valid name. */
  readonly id: string;
  /** Alternative names that resolve to this same subject. */
  readonly aliases?: readonly string[];
  readonly attributes?: Attributes;
}

export interface Position {
  readonly id: string;
  /** Qualification condition a subject must satisfy to hold this position. */
  readonly requires?: PolicyExpr;
}

/**
 * Explicit permission for one subject to hold several of the listed
 * positions at once. Any conflicting pair inside `positions` is covered.
 */
export interface SoDException {
  readonly id: string;
  readonly positions: readonly string[];
  /** Name or alias of the subject the exception applies to. */
  readonly subject: string;
}

export interface Problem {
  readonly positions: readonly Position[];
  readonly subjects: readonly Subject[];
  readonly exceptions?: readonly SoDException[];
  /** Maximum number of exceptions that may be used. Defaults to 0. */
  readonly exceptionBudget?: number;
  /** Hard partial assignment: positionId -> subject name. Must be respected. */
  readonly fixed?: Readonly<Record<string, string>>;
  /**
   * Previous assignment (positionId -> subject name). When present, the
   * solver first minimises the number of positions whose canonical assignee
   * changes, then minimises total cost.
   */
  readonly existingAssignment?: Readonly<Record<string, string>>;
  /** Optional non-negative costs: positionId -> subject name -> cost. Missing entries cost 0. */
  readonly costs?: Readonly<Record<string, Readonly<Record<string, number>>>>;
}

/** positionId -> canonical subject id. */
export type Assignment = Record<string, string>;

export interface HallDeficiency {
  /** A set of positions whose combined qualified-subject pool is too small. */
  readonly positions: readonly string[];
  /** The union of qualified canonical subjects for those positions (|subjects| < |positions|). */
  readonly subjects: readonly string[];
}

export type SolveResult =
  | {
      readonly status: 'feasible';
      readonly assignment: Assignment;
      /** Positions whose canonical assignee differs from `existingAssignment`. */
      readonly changes: number;
      readonly cost: number;
      readonly exceptionsUsed: readonly string[];
    }
  | {
      readonly status: 'infeasible';
      /** Minimum set of positions that must be dropped to restore feasibility. */
      readonly conflictPositions: readonly string[];
      readonly hallDeficiency?: HallDeficiency;
    }
  | {
      readonly status: 'pending';
      readonly reason: string;
      readonly unassigned: readonly string[];
      /** Exceptions a feasible plan would need, when the budget blocked it. */
      readonly requiredExceptions?: readonly string[];
    };

export type VerifyResult =
  | { readonly status: 'valid'; readonly exceptionsUsed: readonly string[] }
  | { readonly status: 'invalid'; readonly violations: readonly string[] }
  | { readonly status: 'pending'; readonly unassigned: readonly string[] };
