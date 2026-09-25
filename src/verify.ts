/**
 * Brute-force verifier.
 *
 * Cross-checks the compiled segment engine against a naive full
 * enumeration of the finite time lattice x attribute domains
 * (the Cartesian expansion the engine itself avoids). Any divergence
 * between the two decision procedures is reported as a mismatch.
 */

import { enumerateAttrs, evalCond, type AttrValue, type Schema } from './conditions.ts';
import type { Decision, PolicyEngine, Rule } from './engine.ts';

export interface Mismatch {
  readonly time: number;
  readonly attrs: Record<string, AttrValue>;
  readonly engineDecision: Decision;
  readonly expectedDecision: Decision;
  readonly engineSource: string | null;
  readonly expectedSource: string | null;
}

export interface VerifyResult {
  readonly checked: number;
  readonly mismatches: readonly Mismatch[];
}

/** Naive reference: scan all rules at every (time, attrs) point. */
export function bruteForceDecide(
  rules: readonly Rule[],
  defaultDecision: Decision,
  time: number,
  attrs: Record<string, AttrValue>,
): { decision: Decision; source: Rule | null } {
  const matching = rules
    .filter((r) => time >= r.start && time < r.end && evalCond(r.condition, attrs))
    .sort((a, b) => {
      if (a.priority !== b.priority) return b.priority - a.priority;
      if (a.version !== b.version) return b.version - a.version;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });
  const top = matching[0];
  return top ? { decision: top.decision, source: top } : { decision: defaultDecision, source: null };
}

/** Full enumeration cross-check over [timeMin, timeMax) and all attribute combos. */
export function verifyAgainstBruteForce(
  engine: PolicyEngine,
  schema: Schema,
  defaultDecision: Decision,
  timeMin: number,
  timeMax: number,
): VerifyResult {
  const rules = engine.listRules();
  const mismatches: Mismatch[] = [];
  let checked = 0;
  for (let time = timeMin; time < timeMax; time++) {
    for (const attrs of enumerateAttrs(schema)) {
      checked++;
      const expected = bruteForceDecide(rules, defaultDecision, time, attrs);
      const actual = engine.query(time, attrs);
      const expectedSource = expected.source ? expected.source.id : null;
      const actualSource = actual.source ? actual.source.ruleId : null;
      if (actual.decision !== expected.decision || actualSource !== expectedSource) {
        mismatches.push({
          time,
          attrs,
          engineDecision: actual.decision,
          expectedDecision: expected.decision,
          engineSource: actualSource,
          expectedSource,
        });
      }
    }
  }
  return { checked, mismatches };
}
