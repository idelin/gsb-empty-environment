import { evalCondition, type AttrValues } from './condition.ts';
import { gradeForScore, type NormalizedPolicy, type NormalizedRule } from './policy.ts';
import { Rational } from './rational.ts';

export const REJECT = 'REJECT' as const;

export type OutcomeKey = string; // grade name or REJECT

export interface EvalResult {
  /** 'definite' when every reachable assignment yields the same outcome. */
  status: 'definite' | 'pending';
  /** The single reachable outcome when status === 'definite', else null. */
  definiteOutcome:
    | { type: 'reject'; ruleIds: readonly string[] }
    | { type: 'grade'; grade: string; score: Rational | null }
    | null;
  /** Every reachable outcome, sorted: grades in band order, REJECT last. */
  possibleOutcomes: ReadonlyArray<{ type: 'reject' } | { type: 'grade'; grade: string }>;
  /**
   * Exact [min, max] of the total score over all non-rejected joint
   * assignments. Correlation between unknown attributes is preserved because
   * bounds come from joint enumeration, never from per-term extrema.
   * Null when every assignment is rejected.
   */
  scoreBounds: { min: Rational; max: Rational } | null;
  /** One concrete assignment witnessing each reachable outcome. */
  witnesses: ReadonlyMap<OutcomeKey, Record<string, number>>;
  /** Rule ids that can trigger a hard reject in some reachable assignment. */
  rejectRuleIds: readonly string[];
  assignmentsExplored: number;
}

export class EvaluationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EvaluationError';
  }
}

export interface EvaluateOptions {
  /** Safety cap on the joint assignment space. Default 1_000_000. */
  maxAssignments?: number;
}

export function unknownAttributes(policy: NormalizedPolicy, known: AttrValues): string[] {
  const out: string[] = [];
  for (const name of policy.attributes.keys()) {
    if (known[name] === undefined) out.push(name);
  }
  return out.sort();
}

export function validateKnownAttributes(policy: NormalizedPolicy, known: AttrValues): void {
  for (const [name, value] of Object.entries(known)) {
    const domain = policy.attributes.get(name);
    if (domain === undefined) {
      throw new EvaluationError(`attribute "${name}" is not declared in the policy`);
    }
    if (!Number.isInteger(value) || !domain.includes(value)) {
      throw new EvaluationError(
        `attribute "${name}" value ${value} is outside its finite domain [${domain.join(', ')}]`,
      );
    }
  }
}

export function assignmentCount(policy: NormalizedPolicy, unknowns: readonly string[]): number {
  let total = 1;
  for (const name of unknowns) total *= policy.attributes.get(name)!.length;
  return total;
}

/** Outcome of one fully-determined assignment. */
export interface AssignmentOutcome {
  rejected: boolean;
  rejectRuleIds: string[];
  score: Rational;
}

/** Evaluates all rules against one complete assignment. */
export function evaluateAssignment(
  rules: readonly NormalizedRule[],
  attrs: AttrValues,
): AssignmentOutcome {
  const rejectRuleIds: string[] = [];
  let score = Rational.zero();
  for (const rule of rules) {
    if (!evalCondition(rule.when, attrs)) continue;
    if (rule.hardReject) rejectRuleIds.push(rule.id);
    else score = score.add(rule.weight);
  }
  return { rejected: rejectRuleIds.length > 0, rejectRuleIds, score };
}

/**
 * Evaluates a policy under partially-known attributes.
 *
 * Missing attributes range over their declared finite domains. Because the
 * joint assignment space is enumerated, unknowns shared by several rules stay
 * correlated: mutually exclusive conditions can never fire together, and the
 * reported score bounds are exact (not interval arithmetic over independent
 * terms). An interval that straddles a threshold therefore yields every
 * reachable grade as 'pending' — never a midpoint guess.
 */
export function evaluate(
  policy: NormalizedPolicy,
  known: AttrValues = {},
  options: EvaluateOptions = {},
): EvalResult {
  validateKnownAttributes(policy, known);
  const unknowns = unknownAttributes(policy, known);
  const total = assignmentCount(policy, unknowns);
  const cap = options.maxAssignments ?? 1_000_000;
  if (total > cap) {
    throw new EvaluationError(
      `joint assignment space too large: ${total} > ${cap}; provide more attributes or raise maxAssignments`,
    );
  }

  const witnesses = new Map<OutcomeKey, Record<string, number>>();
  const reachableGrades = new Set<string>();
  let possiblyRejected = false;
  const rejectRuleIds = new Set<string>();
  let minScore: Rational | null = null;
  let maxScore: Rational | null = null;

  const assignment: Record<string, number> = { ...known };
  const domains = unknowns.map((name) => policy.attributes.get(name)!);

  const visit = (depth: number): void => {
    if (depth === unknowns.length) {
      const outcome = evaluateAssignment(policy.rules, assignment);
      if (outcome.rejected) {
        possiblyRejected = true;
        outcome.rejectRuleIds.forEach((id) => rejectRuleIds.add(id));
        if (!witnesses.has(REJECT)) witnesses.set(REJECT, { ...assignment });
      } else {
        const grade = gradeForScore(policy.bands, outcome.score);
        reachableGrades.add(grade);
        if (!witnesses.has(grade)) witnesses.set(grade, { ...assignment });
        if (minScore === null || outcome.score.lt(minScore)) minScore = outcome.score;
        if (maxScore === null || outcome.score.gt(maxScore)) maxScore = outcome.score;
      }
      return;
    }
    const name = unknowns[depth];
    for (const value of domains[depth]) {
      assignment[name] = value;
      visit(depth + 1);
    }
    delete assignment[name];
  };
  visit(0);

  return assembleResult(policy, {
    witnesses,
    reachableGrades,
    possiblyRejected,
    rejectRuleIds,
    minScore,
    maxScore,
    assignmentsExplored: total,
    rules: policy.rules,
    known,
  });
}

export interface AggregateState {
  witnesses: Map<OutcomeKey, Record<string, number>>;
  reachableGrades: Set<string>;
  possiblyRejected: boolean;
  rejectRuleIds: Set<string>;
  minScore: Rational | null;
  maxScore: Rational | null;
  assignmentsExplored: number;
  rules: readonly NormalizedRule[];
  known: AttrValues;
}

export function assembleResult(policy: NormalizedPolicy, state: AggregateState): EvalResult {
  const gradeOrder = new Map(policy.bands.map((b, i) => [b.grade, i]));
  const possibleOutcomes: Array<{ type: 'reject' } | { type: 'grade'; grade: string }> = [
    ...[...state.reachableGrades]
      .sort((a, b) => gradeOrder.get(a)! - gradeOrder.get(b)!)
      .map((grade) => ({ type: 'grade' as const, grade })),
    ...(state.possiblyRejected ? [{ type: 'reject' as const }] : []),
  ];

  const single = possibleOutcomes.length === 1 ? possibleOutcomes[0] : null;
  let definiteOutcome: EvalResult['definiteOutcome'] = null;
  if (single) {
    if (single.type === 'reject') {
      definiteOutcome = { type: 'reject', ruleIds: [...state.rejectRuleIds].sort() };
    } else {
      // Grade is definite, but the exact score is only definite when every
      // assignment agrees on it; otherwise consult scoreBounds.
      const score = state.minScore!.eq(state.maxScore!) ? state.minScore! : null;
      definiteOutcome = { type: 'grade', grade: single.grade, score };
    }
  }

  return {
    status: single ? 'definite' : 'pending',
    definiteOutcome,
    possibleOutcomes,
    scoreBounds:
      state.minScore === null ? null : { min: state.minScore, max: state.maxScore! },
    witnesses: state.witnesses,
    rejectRuleIds: [...state.rejectRuleIds].sort(),
    assignmentsExplored: state.assignmentsExplored,
  };
}
