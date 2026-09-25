/**
 * Exact evaluation over finite domains.
 *
 * Missing attributes are unknown variables. Because several conditions may
 * read the SAME unknown variable, per-term independent extremes are unsound:
 * the bounds must come from joint assignments so correlations are preserved.
 * We therefore enumerate the joint assignments of the unknown variables that
 * the policy actually reads (all domains are finite) and compute the exact
 * reachable outcome set, exact score bounds, and one witness per outcome.
 */

import type { Attributes, AttributeValue, Schema } from './schema.ts';
import { domainValues, validateAttributes } from './schema.ts';
import { evalCondition } from './condition.ts';
import type { Policy } from './policy.ts';
import { gradeForScore } from './policy.ts';
import type { Rational } from './rational.ts';
import { add, cmp, max, min, toString as ratToString, ZERO } from './rational.ts';

export type Assignment = Readonly<Record<string, AttributeValue>>;

export type Outcome =
  | { kind: 'rejected'; witness: Assignment }
  | { kind: 'grade'; grade: string; score: Rational; witness: Assignment };

export interface EvalResult {
  /** 'definite' when every joint assignment yields the same outcome */
  status: 'definite' | 'undecided';
  /** the single outcome when definite */
  outcome?: Outcome;
  /** all reachable outcomes (one witness each), when undecided */
  outcomes: Outcome[];
  /** exact correlated bounds of the total score over all joint assignments */
  scoreBounds: { min: Rational; max: Rational };
  /** distinct reachable total scores (exact set, may be non-contiguous) */
  reachableScores: Rational[];
  hardRejectPossible: boolean;
  /** unknown variables that were enumerated (the correlated ones) */
  unknownVariables: string[];
  /** number of joint assignments examined */
  assignmentsExplored: number;
}

export interface EvalOptions {
  /** safety cap on the joint-assignment space; default 1_000_000 */
  maxAssignments?: number;
}

function unknownVariablesFor(policy: Policy, attrs: Attributes): string[] {
  const out: string[] = [];
  for (const name of policy.usedAttributes) {
    if (attrs[name] === undefined) out.push(name);
  }
  return out.sort();
}

function* jointAssignments(
  schema: Schema,
  vars: readonly string[],
  maxAssignments: number,
): Generator<Assignment> {
  const domains = vars.map((v) => domainValues(schema[v]));
  let total = 1;
  for (const d of domains) total *= d.length;
  if (total > maxAssignments) {
    throw new Error(
      `joint assignment space too large (${total} > ${maxAssignments}); narrow the domains`,
    );
  }
  const current: Record<string, AttributeValue> = {};
  const rec = function* (i: number): Generator<Assignment> {
    if (i === vars.length) {
      yield { ...current };
      return;
    }
    for (const v of domains[i]) {
      current[vars[i]] = v;
      yield* rec(i + 1);
    }
  };
  yield* rec(0);
}

function outcomeKey(o: Outcome): string {
  return o.kind === 'rejected' ? 'rejected' : `grade:${o.grade}`;
}

/**
 * Exact evaluation: enumerate joint assignments of the unknown variables and
 * record every reachable outcome with a witness. Never uses midpoints or
 * per-term independent extremes to decide a grade.
 */
export function evaluate(policy: Policy, attrs: Attributes, opts: EvalOptions = {}): EvalResult {
  validateAttributes(policy.schema, attrs);
  const maxAssignments = opts.maxAssignments ?? 1_000_000;
  const unknowns = unknownVariablesFor(policy, attrs);

  const byKey = new Map<string, Outcome>();
  const reachable = new Map<string, Rational>();
  let lo: Rational | null = null;
  let hi: Rational | null = null;
  let explored = 0;
  let rejectPossible = false;

  for (const assignment of jointAssignments(policy.schema, unknowns, maxAssignments)) {
    explored++;
    const full: Record<string, AttributeValue> = { ...attrs, ...assignment };
    let score = ZERO;
    let rejected = false;
    for (const rule of policy.rules) {
      const fired = evalCondition(rule.when, full);
      if (fired === true) {
        if (rule.kind === 'reject') rejected = true;
        else score = add(score, rule.weight);
      }
    }
    lo = lo === null ? score : min(lo, score);
    hi = hi === null ? score : max(hi, score);
    reachable.set(ratToString(score), score);
    if (rejected) {
      rejectPossible = true;
      if (!byKey.has('rejected')) byKey.set('rejected', { kind: 'rejected', witness: assignment });
    } else {
      const grade = gradeForScore(policy.thresholds, score);
      const key = `grade:${grade}`;
      if (!byKey.has(key)) byKey.set(key, { kind: 'grade', grade, score, witness: assignment });
    }
  }

  const outcomes = [...byKey.values()];
  const definite = outcomes.length === 1;
  return {
    status: definite ? 'definite' : 'undecided',
    ...(definite ? { outcome: outcomes[0] } : {}),
    outcomes,
    scoreBounds: { min: lo ?? ZERO, max: hi ?? ZERO },
    reachableScores: [...reachable.values()].sort((a, b) => cmp(a, b)),
    hardRejectPossible: rejectPossible,
    unknownVariables: unknowns,
    assignmentsExplored: explored,
  };
}

/**
 * UNSOUND-by-design helper, included only to demonstrate why per-term
 * independent extremes must not be used for grading: it widens each unknown
 * rule's contribution independently, destroying correlations between
 * conditions that share a variable. Tests assert this disagrees with the
 * exact bounds on correlated policies. Never used for grading decisions.
 */
export function naiveIndependentBounds(
  policy: Policy,
  attrs: Attributes,
): { min: Rational; max: Rational } {
  let lo = ZERO;
  let hi = ZERO;
  for (const rule of policy.rules) {
    if (rule.kind !== 'score') continue;
    const fired = evalCondition(rule.when, attrs);
    if (fired === true) {
      lo = add(lo, rule.weight);
      hi = add(hi, rule.weight);
    } else if (fired === 'unknown') {
      // independently assume the worst/best for THIS term only
      if (cmp(rule.weight, ZERO) < 0) lo = add(lo, rule.weight);
      else hi = add(hi, rule.weight);
    }
  }
  return { min: lo, max: hi };
}
