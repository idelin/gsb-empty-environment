/**
 * Incremental engine: keeps per-rule tri-state and aggregate score so that
 * correcting an attribute only re-evaluates the rules that read it. Exact
 * grading still enumerates the joint space of the REMAINING unknown
 * variables, so correlations between shared unknowns are preserved.
 */

import type { AttributeValue, Attributes } from './schema.ts';
import { domainValues, isValidValue } from './schema.ts';
import type { Tri } from './condition.ts';
import { evalCondition } from './condition.ts';
import type { Policy, Rule } from './policy.ts';
import { gradeForScore } from './policy.ts';
import type { Rational } from './rational.ts';
import { add, cmp, sub, toString as ratToString, ZERO } from './rational.ts';
import type { Assignment, EvalResult, Outcome } from './evaluator.ts';

export type AttributeChange = Record<string, AttributeValue | null>; // null = back to unknown

export interface EngineState {
  knownScore: Rational;
  knownRejectFired: boolean;
  unknownRuleIds: string[];
  firedRuleIds: string[];
  reevaluations: number;
}

export class IncrementalEngine {
  private readonly policy: Policy;
  private attrs: Record<string, AttributeValue> = {};
  private readonly states = new Map<string, Tri>();
  private knownScore: Rational = ZERO;
  private knownRejectCount = 0;
  /** diagnostics: how many rule re-evaluations happened (proves incrementality) */
  reevaluations = 0;

  constructor(policy: Policy) {
    this.policy = policy;
    for (const rule of policy.rules) this.states.set(rule.id, 'unknown');
  }

  setAttributes(attrs: Attributes): void {
    this.attrs = {};
    this.knownScore = ZERO;
    this.knownRejectCount = 0;
    for (const [k, v] of Object.entries(attrs)) this.checkValue(k, v);
    this.attrs = { ...attrs };
    for (const rule of this.policy.rules) this.reevaluate(rule);
  }

  /** Apply corrections; only rules reading a changed attribute are re-evaluated. */
  update(changes: AttributeChange): void {
    const changed = new Set<string>();
    for (const [k, v] of Object.entries(changes)) {
      if (v === null) {
        if (!(k in this.attrs)) continue;
        delete this.attrs[k];
      } else {
        this.checkValue(k, v);
        if (this.attrs[k] === v) continue;
        this.attrs[k] = v;
      }
      changed.add(k);
    }
    if (changed.size === 0) return;
    for (const rule of this.policy.rules) {
      let affected = false;
      for (const d of rule.deps) {
        if (changed.has(d)) {
          affected = true;
          break;
        }
      }
      if (affected) this.reevaluate(rule);
    }
  }

  private checkValue(name: string, value: AttributeValue): void {
    const dom = this.policy.schema[name];
    if (!dom) throw new Error(`attribute ${name} is not declared in the schema`);
    if (!isValidValue(dom, value)) {
      throw new Error(`attribute ${name} has out-of-domain value ${JSON.stringify(value)}`);
    }
  }

  private reevaluate(rule: Rule): void {
    this.reevaluations++;
    const prev = this.states.get(rule.id)!;
    const next = evalCondition(rule.when, this.attrs);
    if (prev === next) return;
    this.states.set(rule.id, next);
    if (rule.kind === 'score') {
      if (prev === true) this.knownScore = sub(this.knownScore, rule.weight);
      if (next === true) this.knownScore = add(this.knownScore, rule.weight);
    } else {
      if (prev === true) this.knownRejectCount--;
      if (next === true) this.knownRejectCount++;
    }
  }

  getState(): EngineState {
    const unknownRuleIds: string[] = [];
    const firedRuleIds: string[] = [];
    for (const rule of this.policy.rules) {
      const s = this.states.get(rule.id)!;
      if (s === 'unknown') unknownRuleIds.push(rule.id);
      else if (s === true) firedRuleIds.push(rule.id);
    }
    return {
      knownScore: this.knownScore,
      knownRejectFired: this.knownRejectCount > 0,
      unknownRuleIds,
      firedRuleIds,
      reevaluations: this.reevaluations,
    };
  }

  /**
   * Exact result from the maintained state: the certain part (knownScore /
   * known rejects) plus a joint enumeration over only the unknown variables
   * still referenced by unknown rules.
   */
  evaluate(maxAssignments = 1_000_000): EvalResult {
    const unknownRules = this.policy.rules.filter((r) => this.states.get(r.id) === 'unknown');
    const unknownVars = [
      ...new Set(
        unknownRules.flatMap((r) => [...r.deps].filter((d) => this.attrs[d] === undefined)),
      ),
    ].sort();

    const domains = unknownVars.map((v) => domainValues(this.policy.schema[v]));
    let total = 1;
    for (const d of domains) total *= d.length;
    if (total > maxAssignments) {
      throw new Error(`joint assignment space too large (${total} > ${maxAssignments})`);
    }

    const byKey = new Map<string, Outcome>();
    const reachable = new Map<string, Rational>();
    let lo: Rational | null = null;
    let hi: Rational | null = null;
    let explored = 0;
    let rejectPossible = false;

    const current: Record<string, AttributeValue> = {};
    const rec = (i: number): void => {
      if (i === unknownVars.length) {
        explored++;
        let score = this.knownScore;
        let rejected = this.knownRejectCount > 0;
        for (const rule of unknownRules) {
          const full: Attributes = { ...this.attrs, ...current };
          if (evalCondition(rule.when, full) === true) {
            if (rule.kind === 'reject') rejected = true;
            else score = add(score, rule.weight);
          }
        }
        if (lo === null || cmp(score, lo) < 0) lo = score;
        if (hi === null || cmp(score, hi) > 0) hi = score;
        reachable.set(ratToString(score), score);
        const assignment: Assignment = { ...current };
        if (rejected) {
          rejectPossible = true;
          if (!byKey.has('rejected')) byKey.set('rejected', { kind: 'rejected', witness: assignment });
        } else {
          const grade = gradeForScore(this.policy.thresholds, score);
          const key = `grade:${grade}`;
          if (!byKey.has(key)) byKey.set(key, { kind: 'grade', grade, score, witness: assignment });
        }
        return;
      }
      for (const v of domains[i]) {
        current[unknownVars[i]] = v;
        rec(i + 1);
      }
    };
    rec(0);

    const outcomes = [...byKey.values()];
    const definite = outcomes.length === 1;
    return {
      status: definite ? 'definite' : 'undecided',
      ...(definite ? { outcome: outcomes[0] } : {}),
      outcomes,
      scoreBounds: { min: lo ?? ZERO, max: hi ?? ZERO },
      reachableScores: [...reachable.values()].sort((a, b) => cmp(a, b)),
      hardRejectPossible: rejectPossible,
      unknownVariables: unknownVars,
      assignmentsExplored: explored,
    };
  }
}
