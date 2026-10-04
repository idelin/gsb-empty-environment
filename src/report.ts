import { evaluate, sortByPrecedence } from "./policy";
import { requiredEffects } from "./verifier";
import { Effect, ExceptionRule, Policy, Request } from "./types";
import { SolveResult } from "./solver";

export interface RetainedRuleVerification {
  readonly ruleId: string;
  readonly effect: Effect;
  readonly priority: number;
  readonly decisiveForRequests: number;
  readonly shadowed: boolean;
  readonly constrainedDecisions: number;
  readonly violations: number;
  readonly sampleRequests: readonly Request[];
  readonly summary: string;
}

export interface SolveReport {
  readonly status: "ok";
  readonly totalCost: number;
  readonly revoked: readonly { readonly id: string; readonly cost: number }[];
  readonly verification: {
    readonly exhaustive: true;
    readonly domainSize: number;
    readonly propertyChecks: number;
    readonly violations: 0;
    readonly revocationRounds: number;
  };
  readonly retained: readonly RetainedRuleVerification[];
}

export function buildReport(
  policy: Policy,
  result: Extract<SolveResult, { status: "ok" }>
): SolveReport {
  const keptSorted = sortByPrecedence(result.kept);
  const requests = result.requests;
  const required = requiredEffects(requests, policy.property);

  const decisiveCount = new Map<number, number>();
  const constrainedCount = new Map<number, number>();
  const violationCount = new Map<number, number>();
  const samples = new Map<number, Request[]>();
  let propertyChecks = 0;

  for (let r = 0; r < requests.length; r++) {
    const decision = evaluate(requests[r]!, keptSorted, policy.baseEffect);
    const req = required[r]!;
    if (req !== null) {
      propertyChecks++;
      if (decision.effect !== req) {
        throw new Error(
          `internal error: solved policy violates property at ${JSON.stringify(requests[r])}`
        );
      }
    }
    if (decision.ruleIndex >= 0) {
      const idx = decision.ruleIndex;
      decisiveCount.set(idx, (decisiveCount.get(idx) ?? 0) + 1);
      if (req !== null) {
        constrainedCount.set(idx, (constrainedCount.get(idx) ?? 0) + 1);
        if (decision.effect !== req) {
          violationCount.set(idx, (violationCount.get(idx) ?? 0) + 1);
        }
      }
      const list = samples.get(idx) ?? [];
      if (list.length < 3) {
        list.push(requests[r]!);
        samples.set(idx, list);
      }
    }
  }

  const retained: RetainedRuleVerification[] = keptSorted.map((rule, idx) => {
    const decisive = decisiveCount.get(idx) ?? 0;
    const constrained = constrainedCount.get(idx) ?? 0;
    const violations = violationCount.get(idx) ?? 0;
    const shadowed = decisive === 0;
    const summary = shadowed
      ? `rule "${rule.id}" is fully shadowed by higher-priority rules: it decides 0 of ${requests.length} requests, so retaining it cannot break the property`
      : `rule "${rule.id}" decides ${decisive} of ${requests.length} requests (${constrained} constrained by the property); all ${constrained} constrained decisions satisfy the required effect, verified exhaustively`;
    return {
      ruleId: rule.id,
      effect: rule.effect,
      priority: rule.priority,
      decisiveForRequests: decisive,
      shadowed,
      constrainedDecisions: constrained,
      violations,
      sampleRequests: samples.get(idx) ?? [],
      summary,
    };
  });

  return {
    status: "ok",
    totalCost: result.totalCost,
    revoked: result.revoked,
    verification: {
      exhaustive: true,
      domainSize: requests.length,
      propertyChecks,
      violations: 0,
      revocationRounds: result.rounds,
    },
    retained,
  };
}

export function validatePolicyShape(raw: unknown): Policy {
  if (typeof raw !== "object" || raw === null) {
    throw new Error("policy file must contain a JSON object");
  }
  const p = raw as Record<string, unknown>;
  for (const key of ["baseEffect", "exceptions", "property", "domain"]) {
    if (!(key in p)) throw new Error(`policy is missing "${key}"`);
  }
  if (p.baseEffect !== "allow" && p.baseEffect !== "deny") {
    throw new Error(`baseEffect must be "allow" or "deny"`);
  }
  if (!Array.isArray(p.exceptions) || !Array.isArray(p.property)) {
    throw new Error(`"exceptions" and "property" must be arrays`);
  }
  if (typeof p.domain !== "object" || p.domain === null || Array.isArray(p.domain)) {
    throw new Error(`"domain" must be an object mapping attributes to value lists`);
  }
  for (const [attr, values] of Object.entries(p.domain as Record<string, unknown>)) {
    if (!Array.isArray(values) || values.some((v) => typeof v !== "string")) {
      throw new Error(`domain attribute "${attr}" must be an array of strings`);
    }
  }
  const ruleKeys = ["id", "effect", "priority", "cost", "match"];
  for (const rule of p.exceptions as Record<string, unknown>[]) {
    for (const key of ruleKeys) {
      if (!(key in rule)) throw new Error(`exception is missing "${key}": ${JSON.stringify(rule)}`);
    }
  }
  for (const constraint of p.property as Record<string, unknown>[]) {
    for (const key of ["id", "match", "must"]) {
      if (!(key in constraint))
        throw new Error(`property constraint is missing "${key}": ${JSON.stringify(constraint)}`);
    }
  }
  return raw as Policy;
}

export type { ExceptionRule };
