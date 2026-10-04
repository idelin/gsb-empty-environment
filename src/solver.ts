import { enumerateDomain } from "./domain";
import { matches, sortByPrecedence } from "./policy";
import { requiredEffects } from "./verifier";
import { ExceptionRule, MAX_EXCEPTIONS, Policy, Request } from "./types";

export interface RevokedRule {
  readonly id: string;
  readonly cost: number;
}

export type SolveResult =
  | {
      readonly status: "ok";
      readonly totalCost: number;
      readonly revoked: readonly RevokedRule[];
      readonly kept: readonly ExceptionRule[];
      readonly sortedRules: readonly ExceptionRule[];
      readonly requests: readonly Request[];
      readonly rounds: number;
    }
  | { readonly status: "infeasible"; readonly reason: string }
  | { readonly status: "too-many-exceptions"; readonly count: number };

export function solve(policy: Policy): SolveResult {
  const n = policy.exceptions.length;
  if (n > MAX_EXCEPTIONS) {
    return { status: "too-many-exceptions", count: n };
  }
  for (const rule of policy.exceptions) {
    if (!Number.isFinite(rule.cost) || rule.cost < 0) {
      throw new Error(`exception "${rule.id}" has invalid cost ${rule.cost}`);
    }
  }

  const rules = sortByPrecedence(policy.exceptions);
  const requests = enumerateDomain(policy.domain);
  const required = requiredEffects(requests, policy.property);
  const base = policy.baseEffect;

  const reqMatches: number[][] = requests.map(() => []);
  for (let i = 0; i < n; i++) {
    const match = rules[i]!.match;
    for (let r = 0; r < requests.length; r++) {
      if (matches(requests[r]!, match)) {
        reqMatches[r]!.push(i);
      }
    }
  }

  const constrained: number[] = [];
  for (let r = 0; r < requests.length; r++) {
    const req = required[r]!;
    if (req === null) continue;
    constrained.push(r);
    if (reqMatches[r]!.length === 0 && base !== req) {
      return {
        status: "infeasible",
        reason: `request ${JSON.stringify(
          requests[r]
        )} requires "${req}" but no exception matches it and the base policy is "${base}"`,
      };
    }
    if (base !== req && !reqMatches[r]!.some((i) => rules[i]!.effect === req)) {
      return {
        status: "infeasible",
        reason: `request ${JSON.stringify(
          requests[r]
        )} requires "${req}" but neither the base policy nor any exception can produce it`,
      };
    }
  }

  const kept = new Array<boolean>(n).fill(true);
  let rounds = 0;
  let changed = true;
  while (changed) {
    changed = false;
    rounds++;
    for (const r of constrained) {
      const req = required[r]!;
      let decider = -1;
      for (const i of reqMatches[r]!) {
        if (kept[i]) {
          decider = i;
          break;
        }
      }
      const effect = decider === -1 ? base : rules[decider]!.effect;
      if (effect !== req) {
        if (decider === -1) {
          throw new Error(
            `internal error: no surviving exception can satisfy ${JSON.stringify(requests[r])}`
          );
        }
        kept[decider] = false;
        changed = true;
      }
    }
  }

  const revoked: RevokedRule[] = [];
  const keptRules: ExceptionRule[] = [];
  let totalCost = 0;
  for (let i = 0; i < n; i++) {
    if (kept[i]) {
      keptRules.push(rules[i]!);
    } else {
      revoked.push({ id: rules[i]!.id, cost: rules[i]!.cost });
      totalCost += rules[i]!.cost;
    }
  }

  return {
    status: "ok",
    totalCost,
    revoked,
    kept: keptRules,
    sortedRules: rules,
    requests,
    rounds,
  };
}
