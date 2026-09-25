/**
 * Policies: ordered rule lists mapping an input to a judgment label.
 * The "judgment difference" is the disagreement between two policy versions
 * on the same input. Policies are pure functions of the explicit input
 * attributes only (see condition.ts); no external identity services.
 */
import { evalCondition, type Condition, type Input } from './condition.ts';

export interface Rule {
  when: Condition;
  label: string;
}

export interface Policy {
  name: string;
  /** Bumping the rule version invalidates cached oracle results. */
  version: string;
  rules: Rule[];
  default: string;
}

export function judge(policy: Policy, input: Input): string {
  for (const rule of policy.rules) {
    if (evalCondition(rule.when, input)) return rule.label;
  }
  return policy.default;
}

export type OracleVerdict = 'diff' | 'same';

export interface Oracle {
  /** Only called for schema-valid inputs. */
  (input: Input): OracleVerdict;
}

/** Differential oracle: difference between two policy versions must persist. */
export function differentialOracle(a: Policy, b: Policy): Oracle {
  return (input) => (judge(a, input) !== judge(b, input) ? 'diff' : 'same');
}
