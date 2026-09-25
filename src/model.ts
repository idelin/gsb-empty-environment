/**
 * Domain model for the pair-wise separation-of-duties (SoD) checker.
 *
 * Everything here is a finite, explicitly declared structure: there is no
 * external identity service. A subject is an opaque entity that may be known
 * under several aliases; positions carry qualification conditions over
 * finite-domain attributes; exceptions carry an integer cost and the global
 * exception budget bounds how much exception cost a single plan may use.
 */

/** Value types allowed inside explicit subject attributes. */
export type AttrValue = string | number | boolean;
export type Attrs = Record<string, AttrValue>;

/**
 * Declarative condition language.
 * - finite-set membership / equality of attributes
 * - integer comparison (lte / gte)
 * - boolean composition (and / or / not)
 *
 * An unknown attribute or a type mismatch evaluates to false (fail closed);
 * `always` is the trivial true condition.
 */
export type Condition =
  | { kind: "always" }
  | { kind: "eq"; attr: string; value: AttrValue }
  | { kind: "in"; attr: string; values: AttrValue[] }
  | { kind: "lte"; attr: string; value: number }
  | { kind: "gte"; attr: string; value: number }
  | { kind: "and"; parts: Condition[] }
  | { kind: "or"; parts: Condition[] }
  | { kind: "not"; part: Condition };

export interface Subject {
  /** Canonical identifier of this entity. */
  id: string;
  /** Explicit attributes only — no attribute is ever looked up externally. */
  attrs?: Attrs;
}

export interface Position {
  id: string;
  /** Required qualification; evaluated against the assignee's attributes. */
  required: Condition;
  /**
   * Optional explicit candidate pool, including the preferred assignee and
   * substitutes. When omitted every eligible subject is a candidate.
   * Entries may be canonical ids or aliases.
   */
  candidates?: string[];
}

/**
 * A pair-wise separation rule: every subject may occupy at most one position
 * in the group, unless an exception explicitly covers that subject.
 */
export interface SodGroup {
  id: string;
  positions: string[];
}

/**
 * An explicit exception that permits one subject to hold multiple positions
 * of one SoD group. `subject` may be a canonical id or any alias of the
 * subject (it is canonicalised by the resolver). Cost is a non-negative
 * integer.
 */
export interface Exception {
  id: string;
  group: string;
  subject: string;
  cost?: number;
}

export interface Problem {
  subjects: Subject[];
  /**
   * Maps an alias to the canonical subject id it denotes. Chains are
   * followed. An alias must never equal a canonical subject id.
   */
  aliases?: Record<string, string>;
  positions: Position[];
  sodGroups?: SodGroup[];
  exceptions?: Exception[];
  /** Non-negative integer total budget of exception cost. Defaults to 0. */
  exceptionBudget?: number;
  /**
   * Assignment cost per (positionId, canonicalSubjectId). Missing entries
   * default to 0. Costs are non-negative integers.
   */
  costs?: Record<string, Record<string, number>>;
}

/** positionId -> canonical subject id (or null for "not assigned yet"). */
export type Assignment = Record<string, string | null>;

export type Verdict = "approved" | "rejected" | "undecided";

export type ViolationKind =
  | "unknown-subject"
  | "ineligible"
  | "outside-candidate-pool"
  | "sod-conflict"
  | "unknown-position";

export interface Violation {
  kind: ViolationKind;
  position: string;
  subject?: string;
  group?: string;
  otherPositions?: string[];
}

export interface ExceptionUse {
  exceptionId: string;
  group: string;
  subject: string;
  cost: number;
  positions: string[];
}

export interface CheckResult {
  verdict: Verdict;
  /** Hard rule violations. Any one makes the verdict "rejected". */
  violations: Violation[];
  /** Unassigned positions. Any one makes the verdict "undecided" at best. */
  unassigned: string[];
  exceptionsUsed: ExceptionUse[];
  exceptionCost: number;
  /**
   * For a partial assignment: true when it can be extended to a complete,
   * budget-respecting assignment. Absent for complete assignments.
   */
  extendable?: boolean;
  /** A witness completion when `extendable` is true. */
  witness?: Assignment;
  reasons: string[];
}

export type SolveStatus = "satisfied" | "undecided" | "infeasible";

export interface SolveResult {
  status: SolveStatus;
  assignment: Assignment | null;
  exceptionsUsed: ExceptionUse[];
  exceptionCost: number;
  assignmentCost: number;
  changesFromPrevious: number;
  /**
   * Minimum-cardinality set of positions whose constraints cannot be jointly
   * honoured (hard conflict core). Populated for "infeasible".
   */
  conflictSet: string[] | null;
  reasons: string[];
}
