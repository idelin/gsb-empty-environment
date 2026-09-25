import { evalCondition, type AttrValues } from './condition.ts';
import {
  assembleResult,
  assignmentCount,
  unknownAttributes,
  validateKnownAttributes,
  EvaluationError,
  REJECT,
  type AggregateState,
  type EvalResult,
  type EvaluateOptions,
  type OutcomeKey,
} from './engine.ts';
import {
  gradeForScore,
  normalizePolicy,
  type NormalizedPolicy,
  type NormalizedRule,
  type PolicySpec,
} from './policy.ts';
import { Rational } from './rational.ts';

export interface SessionStats {
  /** Static rule (re-)evaluations since session creation. */
  staticRuleEvals: number;
  /** Dynamic rule evaluations performed inside enumeration. */
  enumRuleEvals: number;
  /** Joint assignments actually enumerated. */
  assignmentsEnumerated: number;
  /** evaluate() calls that reused the cached enumeration unchanged. */
  enumerationReuses: number;
}

interface DynOutcome {
  rejectIds: string[];
  score: Rational;
  witness: Record<string, number>;
}

interface EnumCache {
  signature: string;
  /** Distinct dynamic outcomes, deduplicated, each with a witness assignment. */
  outcomes: DynOutcome[];
}

/**
 * Incremental grading session. Holds a normalized policy plus the current
 * attribute assignment; `evaluate()` recomputes only what the latest
 * corrections can affect:
 *
 *  - rules that read only known attributes are cached per rule and
 *    re-evaluated only when one of their own dependencies changed;
 *  - the joint enumeration over unknown attributes is reused verbatim when
 *    neither the unknown set nor any known value feeding a dynamic rule
 *    changed since the previous evaluation.
 */
export class GradingSession {
  readonly policy: NormalizedPolicy;
  readonly stats: SessionStats = {
    staticRuleEvals: 0,
    enumRuleEvals: 0,
    assignmentsEnumerated: 0,
    enumerationReuses: 0,
  };

  private known: Record<string, number> = {};
  private dirty = new Set<string>();
  private staticCache = new Map<string, boolean>(); // ruleId -> fired
  private enumCache: EnumCache | null = null;

  constructor(policy: NormalizedPolicy | PolicySpec, initial: AttrValues = {}) {
    this.policy = isNormalized(policy) ? policy : normalizePolicy(policy);
    this.setAttributes(initial);
  }

  setAttribute(name: string, value: number): this {
    if (!this.policy.attributes.has(name)) {
      throw new EvaluationError(`attribute "${name}" is not declared in the policy`);
    }
    if (this.known[name] !== value) {
      this.known[name] = value;
      this.dirty.add(name);
    }
    return this;
  }

  setAttributes(values: AttrValues): this {
    for (const [k, v] of Object.entries(values)) this.setAttribute(k, v);
    return this;
  }

  /** Marks a previously known attribute as missing again. */
  removeAttribute(name: string): this {
    if (name in this.known) {
      delete this.known[name];
      this.dirty.add(name);
    }
    return this;
  }

  currentAttributes(): Readonly<Record<string, number>> {
    return { ...this.known };
  }

  evaluate(options: EvaluateOptions = {}): EvalResult {
    validateKnownAttributes(this.policy, this.known);
    const unknowns = unknownAttributes(this.policy, this.known);
    const unknownSet = new Set(unknowns);

    const staticRules: NormalizedRule[] = [];
    const dynamicRules: NormalizedRule[] = [];
    for (const rule of this.policy.rules) {
      let touchesUnknown = false;
      for (const a of rule.attrs) if (unknownSet.has(a)) { touchesUnknown = true; break; }
      (touchesUnknown ? dynamicRules : staticRules).push(rule);
    }

    // --- static part: cached per rule, invalidated by dirty dependencies ---
    let staticScore = Rational.zero();
    const staticRejects: string[] = [];
    for (const rule of staticRules) {
      let fired = this.staticCache.get(rule.id);
      let stale = fired === undefined;
      if (!stale) {
        for (const a of rule.attrs) if (this.dirty.has(a)) { stale = true; break; }
      }
      if (stale) {
        fired = evalCondition(rule.when, this.known);
        this.staticCache.set(rule.id, fired);
        this.stats.staticRuleEvals++;
      }
      if (fired) {
        if (rule.hardReject) staticRejects.push(rule.id);
        else staticScore = staticScore.add(rule.weight);
      }
    }

    // --- dynamic part: joint enumeration over unknowns, reused when possible ---
    let dynOutcomes: DynOutcome[];
    if (dynamicRules.length === 0) {
      dynOutcomes = [{ rejectIds: [], score: Rational.zero(), witness: { ...this.known } }];
    } else {
      const signature = this.enumSignature(unknowns, dynamicRules);
      if (this.enumCache && this.enumCache.signature === signature) {
        this.stats.enumerationReuses++;
        dynOutcomes = this.enumCache.outcomes;
      } else {
        dynOutcomes = this.enumerate(unknowns, dynamicRules, options);
        this.enumCache = { signature, outcomes: dynOutcomes };
      }
    }

    // --- combine static offset with every distinct dynamic outcome ---
    const state: AggregateState = {
      witnesses: new Map<OutcomeKey, Record<string, number>>(),
      reachableGrades: new Set<string>(),
      possiblyRejected: false,
      rejectRuleIds: new Set<string>(),
      minScore: null,
      maxScore: null,
      assignmentsExplored:
        dynamicRules.length === 0 ? 1 : assignmentCount(this.policy, unknowns),
      rules: this.policy.rules,
      known: this.known,
    };
    const staticReject = staticRejects.length > 0;
    for (const id of staticRejects) state.rejectRuleIds.add(id);
    for (const dyn of dynOutcomes) {
      const rejected = staticReject || dyn.rejectIds.length > 0;
      if (rejected) {
        state.possiblyRejected = true;
        for (const id of dyn.rejectIds) state.rejectRuleIds.add(id);
        if (!state.witnesses.has(REJECT)) state.witnesses.set(REJECT, dyn.witness);
        continue;
      }
      const score = staticScore.add(dyn.score);
      const grade = gradeForScore(this.policy.bands, score);
      state.reachableGrades.add(grade);
      if (!state.witnesses.has(grade)) state.witnesses.set(grade, dyn.witness);
      if (state.minScore === null || score.lt(state.minScore)) state.minScore = score;
      if (state.maxScore === null || score.gt(state.maxScore)) state.maxScore = score;
    }
    if (staticReject) {
      // A static hard reject dominates every dynamic outcome; witnesses all
      // point at REJECT, grades are unreachable.
      state.reachableGrades.clear();
      state.minScore = null;
      state.maxScore = null;
      state.witnesses.forEach((_, key) => {
        if (key !== REJECT) state.witnesses.delete(key);
      });
    }

    this.dirty.clear();
    return assembleResult(this.policy, state);
  }

  private enumSignature(unknowns: readonly string[], dynamicRules: readonly NormalizedRule[]): string {
    const feedingKnown = new Set<string>();
    for (const rule of dynamicRules) {
      for (const a of rule.attrs) if (this.known[a] !== undefined) feedingKnown.add(a);
    }
    const knownPart = [...feedingKnown].sort().map((n) => [n, this.known[n]]);
    return JSON.stringify({ u: unknowns, k: knownPart });
  }

  private enumerate(
    unknowns: readonly string[],
    dynamicRules: readonly NormalizedRule[],
    options: EvaluateOptions,
  ): DynOutcome[] {
    const total = assignmentCount(this.policy, unknowns);
    const cap = options.maxAssignments ?? 1_000_000;
    if (total > cap) {
      throw new EvaluationError(
        `joint assignment space too large: ${total} > ${cap}; provide more attributes or raise maxAssignments`,
      );
    }
    const dedup = new Map<string, DynOutcome>();
    const assignment: Record<string, number> = { ...this.known };
    const domains = unknowns.map((n) => this.policy.attributes.get(n)!);

    const visit = (depth: number): void => {
      if (depth === unknowns.length) {
        const rejectIds: string[] = [];
        let score = Rational.zero();
        for (const rule of dynamicRules) {
          this.stats.enumRuleEvals++;
          if (!evalCondition(rule.when, assignment)) continue;
          if (rule.hardReject) rejectIds.push(rule.id);
          else score = score.add(rule.weight);
        }
        this.stats.assignmentsEnumerated++;
        const key = `${rejectIds.slice().sort().join(',')}|${score.toString()}`;
        if (!dedup.has(key)) {
          dedup.set(key, { rejectIds, score, witness: { ...assignment } });
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
    return [...dedup.values()];
  }
}

function isNormalized(p: NormalizedPolicy | PolicySpec): p is NormalizedPolicy {
  return (p as NormalizedPolicy).attributes instanceof Map;
}
