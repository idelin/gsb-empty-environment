/** Input snapshot: the immutable field/value record every condition reads. */
export type Snapshot = Readonly<Record<string, unknown>>;

/**
 * A condition is evaluated exclusively against the input snapshot.
 * It never sees assignments produced by other rules in the same round,
 * because the engine hands it the pristine snapshot only.
 */
export interface Condition {
  readonly description: string;
  evaluate(snapshot: Snapshot): boolean;
  /** Structured account of the evaluation, used in evidence trees. */
  explain(snapshot: Snapshot): ConditionNode;
}

export interface ConditionNode {
  readonly kind: string;
  readonly summary: string;
  readonly result: boolean;
  readonly children?: readonly ConditionNode[];
}

export interface Rule {
  readonly id: string;
  /** Higher priority wins when several rules assign the same field. */
  readonly priority: number;
  readonly condition: Condition;
  /** Fields this rule writes when its condition matches the snapshot. */
  readonly assigns: Readonly<Record<string, unknown>>;
}

/** Phase-1 evidence: how one rule's condition fared against the snapshot. */
export interface RuleEvaluation {
  readonly ruleId: string;
  readonly priority: number;
  readonly matched: boolean;
  readonly condition: ConditionNode;
}

export type CandidateOutcome =
  | 'winner' // supplied the winning value
  | 'shadowed' // lost to a strictly higher priority
  | 'conflict' // tied at top priority with a different value
  | 'agreed'; // tied at top priority with the same value as the winner

export interface CandidateNode {
  readonly ruleId: string;
  readonly priority: number;
  readonly value: unknown;
  readonly outcome: CandidateOutcome;
}

export interface ConflictEntry {
  readonly ruleId: string;
  readonly value: unknown;
}

/** Per-field evidence tree: root is the field, children are candidates. */
export interface FieldEvidence {
  readonly field: string;
  readonly status: 'resolved' | 'conflict' | 'untouched';
  readonly winner?: { readonly ruleId: string; readonly priority: number; readonly value: unknown };
  readonly conflict?: { readonly priority: number; readonly entries: readonly ConflictEntry[] };
  readonly candidates: readonly CandidateNode[];
}

export interface Conflict {
  readonly field: string;
  readonly priority: number;
  readonly entries: readonly ConflictEntry[];
}

export interface EvalResult {
  /** Snapshot with winning assignments applied. Conflicted fields keep their snapshot value. */
  readonly output: Record<string, unknown>;
  /** Phase-1 evidence for every rule, in input order. */
  readonly rules: readonly RuleEvaluation[];
  /** Per-field evidence trees, sorted by field name. */
  readonly fields: readonly FieldEvidence[];
  readonly conflicts: readonly Conflict[];
}
