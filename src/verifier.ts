import { matches, evaluate, sortByPrecedence } from "./policy";
import { Effect, ExceptionRule, Policy, PropertyConstraint, Request } from "./types";

export interface Violation {
  readonly request: Request;
  readonly constraintId: string;
  readonly required: Effect;
  readonly actual: Effect;
}

export interface VerificationResult {
  readonly ok: boolean;
  readonly checkedRequests: number;
  readonly violations: readonly Violation[];
}

export function requiredEffects(
  requests: readonly Request[],
  property: readonly PropertyConstraint[]
): (Effect | null)[] {
  const required: (Effect | null)[] = new Array(requests.length).fill(null);
  for (let r = 0; r < requests.length; r++) {
    for (const constraint of property) {
      if (!matches(requests[r]!, constraint.match)) continue;
      const existing = required[r];
      if (existing !== null && existing !== constraint.must) {
        throw new Error(
          `property is self-contradictory for request ${JSON.stringify(
            requests[r]
          )}: constraints require both "${existing}" and "${constraint.must}"`
        );
      }
      required[r] = constraint.must;
    }
  }
  return required;
}

export function verifyPolicy(
  keptRules: readonly ExceptionRule[],
  baseEffect: Effect,
  property: readonly PropertyConstraint[],
  requests: readonly Request[]
): VerificationResult {
  const sorted = sortByPrecedence(keptRules);
  const required = requiredEffects(requests, property);
  const violations: Violation[] = [];
  for (let r = 0; r < requests.length; r++) {
    const req = required[r]!;
    if (req === null) continue;
    const decision = evaluate(requests[r]!, sorted, baseEffect);
    if (decision.effect !== req) {
      violations.push({
        request: requests[r]!,
        constraintId: "",
        required: req,
        actual: decision.effect,
      });
    }
  }
  return { ok: violations.length === 0, checkedRequests: requests.length, violations };
}

export function verifyPolicyFull(
  policy: Policy,
  keptRules: readonly ExceptionRule[],
  requests: readonly Request[]
): VerificationResult {
  return verifyPolicy(keptRules, policy.baseEffect, policy.property, requests);
}
