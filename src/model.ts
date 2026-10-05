/**
 * Policy model: a base policy plus temporary allow/deny exception rules.
 * Evaluation is first-match by priority (higher priority first).
 */

export type Effect = "allow" | "deny";

/** A condition maps attribute names to the set of accepted values. Missing keys are wildcards. */
export interface Condition {
  [attribute: string]: readonly string[];
}

export interface Rule {
  id: string;
  effect: Effect;
  priority: number;
  when: Condition;
}

export interface ExceptionRule extends Rule {
  /** Non-negative cost paid to revoke this exception. */
  cost: number;
  note?: string;
}

export interface Policy {
  /** Decision when no rule matches. */
  defaultEffect: Effect;
  baseRules: readonly Rule[];
  exceptions: readonly ExceptionRule[];
}

export interface SafetyProperty {
  /** Every request matching `when` must be decided as `mustBe`. */
  when: Condition;
  mustBe: Effect;
  description: string;
}

export interface Scenario {
  name: string;
  /** Finite input domain: every attribute and its finite set of values. */
  domains: Record<string, readonly string[]>;
  policy: Policy;
  property: SafetyProperty;
}

export interface Request {
  [attribute: string]: string;
}

export const MAX_EXCEPTIONS = 24;

export function matches(cond: Condition, req: Request): boolean {
  for (const key of Object.keys(cond)) {
    const value = req[key];
    if (value === undefined || !cond[key].includes(value)) return false;
  }
  return true;
}

/** Deterministic rule ordering: priority desc, then id asc as tie-break. */
export function orderRules<T extends Rule>(rules: readonly T[]): T[] {
  return [...rules].sort((a, b) => b.priority - a.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

export interface Decision {
  effect: Effect;
  /** The rule that decided, or null when the policy default applied. */
  decidedBy: Rule | null;
}

/** Evaluate the base policy alone (no exceptions). */
export function evaluateBase(policy: Policy, req: Request): Decision {
  for (const rule of orderRules(policy.baseRules)) {
    if (matches(rule.when, req)) return { effect: rule.effect, decidedBy: rule };
  }
  return { effect: policy.defaultEffect, decidedBy: null };
}

/** Evaluate base policy plus the retained exceptions (first match by priority). */
export function evaluate(policy: Policy, retained: readonly ExceptionRule[], req: Request): Decision {
  const rules = orderRules([...policy.baseRules, ...retained]);
  for (const rule of rules) {
    if (matches(rule.when, req)) return { effect: rule.effect, decidedBy: rule };
  }
  return { effect: policy.defaultEffect, decidedBy: null };
}

/** Enumerate the full finite input domain as a cross product, in deterministic order. */
export function enumerateRequests(domains: Record<string, readonly string[]>): Request[] {
  const attrs = Object.keys(domains);
  const out: Request[] = [];
  const current: Request = {};
  const walk = (i: number): void => {
    if (i === attrs.length) {
      out.push({ ...current });
      return;
    }
    for (const value of domains[attrs[i]]) {
      current[attrs[i]] = value;
      walk(i + 1);
    }
  };
  walk(0);
  return out;
}

/** Validate a scenario; throws on structural problems. */
export function validateScenario(scenario: Scenario): void {
  const { policy } = scenario;
  if (policy.exceptions.length > MAX_EXCEPTIONS) {
    throw new Error(`too many exceptions: ${policy.exceptions.length} > ${MAX_EXCEPTIONS}`);
  }
  const ids = new Set<string>();
  for (const rule of [...policy.baseRules, ...policy.exceptions]) {
    if (ids.has(rule.id)) throw new Error(`duplicate rule id: ${rule.id}`);
    ids.add(rule.id);
  }
  for (const ex of policy.exceptions) {
    if (!Number.isFinite(ex.cost) || ex.cost < 0) {
      throw new Error(`exception ${ex.id} has invalid cost ${ex.cost}`);
    }
  }
  for (const [attr, values] of Object.entries(scenario.domains)) {
    if (values.length === 0) throw new Error(`domain ${attr} is empty`);
  }
}
