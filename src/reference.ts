/**
 * Independent exhaustive reference implementation, written as plainly as
 * possible on purpose. The test-suite cross-checks the main evaluator and the
 * incremental engine against this brute force on every case, including
 * randomized policies with negative weights, hard rejects and mutually
 * exclusive conditions.
 */

import type { AttributeValue, Attributes } from './schema.ts';
import { domainValues, validateAttributes } from './schema.ts';
import { evalCondition } from './condition.ts';
import type { Policy } from './policy.ts';
import { gradeForScore } from './policy.ts';
import type { Rational } from './rational.ts';
import { add, cmp, toString as ratToString, ZERO } from './rational.ts';

export interface ReferenceResult {
  /** for each joint assignment: total score, reject flag, grade (if not rejected) */
  rows: Array<{
    assignment: Record<string, AttributeValue>;
    score: Rational;
    rejected: boolean;
    grade: string | null;
  }>;
  reachableOutcomes: string[]; // 'rejected' | 'grade:<g>'
  reachableScores: string[];   // canonical rational strings
}

export function referenceEvaluate(policy: Policy, attrs: Attributes): ReferenceResult {
  validateAttributes(policy.schema, attrs);
  // enumerate ALL schema attributes not provided (not only used ones) — the
  // result must be identical, which is itself part of the cross-check
  const unknowns = Object.keys(policy.schema)
    .filter((k) => attrs[k] === undefined)
    .sort();
  const domains = unknowns.map((k) => domainValues(policy.schema[k]));

  const rows: ReferenceResult['rows'] = [];
  const outcomes = new Set<string>();
  const scores = new Map<string, (typeof rows)[number]['score']>();

  const rec = (i: number, current: Record<string, AttributeValue>): void => {
    if (i === unknowns.length) {
      const full: Record<string, AttributeValue> = { ...attrs, ...current };
      let score = ZERO;
      let rejected = false;
      for (const rule of policy.rules) {
        if (evalCondition(rule.when, full) === true) {
          if (rule.kind === 'reject') rejected = true;
          else score = add(score, rule.weight);
        }
      }
      const grade = rejected ? null : gradeForScore(policy.thresholds, score);
      rows.push({ assignment: { ...current }, score, rejected, grade });
      outcomes.add(rejected ? 'rejected' : `grade:${grade}`);
      scores.set(ratToString(score), score);
      return;
    }
    for (const v of domains[i]) {
      current[unknowns[i]] = v;
      rec(i + 1, current);
    }
  };
  rec(0, {});

  return {
    rows,
    reachableOutcomes: [...outcomes].sort(),
    reachableScores: [...scores.entries()]
      .sort((a, b) => cmp(a[1], b[1]))
      .map(([k]) => k),
  };
}
