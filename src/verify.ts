/**
 * Exhaustive verification over the whole finite input domain.
 *
 * After the solver picks a revocation set, we re-check the safety property
 * by brute force: every request in the finite domain is evaluated against
 * the post-revocation policy. We also produce a per-retained-exception
 * summary proving that each kept exception never decides a
 * property-relevant request unsafely, and a per-revoked-exception
 * necessity certificate (a witness request that breaks if it were kept).
 */

import { enumerateRequests, evaluate, matches } from "./model.ts";
import type { ExceptionRule, Request, Scenario } from "./model.ts";
import { formatRequest, retainedExceptions } from "./solver.ts";
import type { RevocationPlan } from "./solver.ts";

export interface RetainedExceptionSummary {
  id: string;
  effect: string;
  priority: number;
  cost: number;
  /** Requests in the domain where this exception is the deciding rule. */
  decidesRequests: number;
  /** Of those, how many are property-relevant. */
  propertyRelevant: number;
  /** Of those, how many violate the property (must be 0). */
  propertyViolations: number;
  ok: boolean;
}

export interface RevokedExceptionSummary {
  id: string;
  effect: string;
  priority: number;
  cost: number;
  /** Witness request proving this revocation is necessary, if available. */
  witness: Request | null;
  /** Re-evaluating the witness with this exception restored violates the property. */
  necessityChecked: boolean;
}

export interface VerificationReport {
  propertyHolds: boolean;
  totalRequests: number;
  propertyRelevantRequests: number;
  violationsBefore: number;
  violationsAfter: number;
  violatingRequests: Request[];
  retained: RetainedExceptionSummary[];
  revoked: RevokedExceptionSummary[];
}

export function verifyPlan(scenario: Scenario, plan: RevocationPlan): VerificationReport {
  const { policy, property } = scenario;
  const retained = retainedExceptions(scenario, plan.revoked);
  const requests = enumerateRequests(scenario.domains);

  // Tally decisions made by each retained exception under the final policy.
  const decides = new Map<string, { total: number; relevant: number; violations: number }>();
  for (const ex of retained) decides.set(ex.id, { total: 0, relevant: 0, violations: 0 });

  let relevant = 0;
  let violationsAfter = 0;
  let violationsBefore = 0;
  const violatingRequests: Request[] = [];

  for (const req of requests) {
    if (!matches(property.when, req)) continue;
    relevant++;

    const after = evaluate(policy, retained, req);
    if (after.effect !== property.mustBe) {
      violationsAfter++;
      if (violatingRequests.length < 8) violatingRequests.push(req);
    }
    const before = evaluate(policy, [...policy.exceptions], req);
    if (before.effect !== property.mustBe) violationsBefore++;

    if (after.decidedBy && decides.has(after.decidedBy.id)) {
      const tally = decides.get(after.decidedBy.id)!;
      tally.total++;
      tally.relevant++;
      if (after.effect !== property.mustBe) tally.violations++;
    }
  }

  // Non-relevant decisions made by each retained exception (for context).
  const retainedSummaries: RetainedExceptionSummary[] = retained.map((ex) => {
    const tally = decides.get(ex.id)!;
    let totalDecisions = 0;
    for (const req of requests) {
      const d = evaluate(policy, retained, req);
      if (d.decidedBy && d.decidedBy.id === ex.id) totalDecisions++;
    }
    return {
      id: ex.id,
      effect: ex.effect,
      priority: ex.priority,
      cost: ex.cost,
      decidesRequests: totalDecisions,
      propertyRelevant: tally.relevant,
      propertyViolations: tally.violations,
      ok: tally.violations === 0,
    };
  });

  // Necessity certificates for revoked exceptions.
  const revokedSet = new Set(plan.revoked);
  const revokedSummaries: RevokedExceptionSummary[] = plan.revoked.map((idx) => {
    const ex: ExceptionRule = policy.exceptions[idx];
    const witness = plan.witnesses.get(idx) ?? null;
    let necessityChecked = false;
    if (witness) {
      // Restore only this exception; the witness request must become unsafe.
      const restored = retainedExceptions(scenario, [...revokedSet].filter((i) => i !== idx));
      necessityChecked = evaluate(policy, restored, witness).effect !== property.mustBe;
    }
    return {
      id: ex.id,
      effect: ex.effect,
      priority: ex.priority,
      cost: ex.cost,
      witness,
      necessityChecked: witness !== null && necessityChecked,
    };
  });

  return {
    propertyHolds: violationsAfter === 0,
    totalRequests: requests.length,
    propertyRelevantRequests: relevant,
    violationsBefore,
    violationsAfter,
    violatingRequests,
    retained: retainedSummaries,
    revoked: revokedSummaries,
  };
}

export function renderReport(scenario: Scenario, plan: RevocationPlan, report: VerificationReport): string {
  const lines: string[] = [];
  const { property } = scenario;
  lines.push(`Scenario: ${scenario.name}`);
  lines.push(`Property: ${property.description} (must be "${property.mustBe}")`);
  lines.push(
    `Domain: ${report.totalRequests} requests, ${report.propertyRelevantRequests} property-relevant`,
  );
  lines.push(
    `Violations before revocation: ${report.violationsBefore}; after: ${report.violationsAfter}`,
  );
  lines.push(
    `Optimal revocation set: ${plan.revoked.length} exception(s), total cost ${plan.totalCost}`,
  );
  lines.push("");
  lines.push("Revoked exceptions (with necessity certificates):");
  if (report.revoked.length === 0) lines.push("  (none)");
  for (const r of report.revoked) {
    const cert = r.witness
      ? r.necessityChecked
        ? `keeping it breaks the property e.g. on [${formatRequest(r.witness)}]`
        : `certificate FAILED to validate on [${formatRequest(r.witness)}]`
      : "no witness recorded";
    lines.push(`  - ${r.id} (${r.effect}, priority ${r.priority}, cost ${r.cost}): ${cert}`);
  }
  lines.push("");
  lines.push("Retained exceptions (verified over the full finite domain):");
  if (report.retained.length === 0) lines.push("  (none)");
  for (const r of report.retained) {
    const verdict = r.ok ? "OK" : "BREAKS PROPERTY";
    lines.push(
      `  - ${r.id} (${r.effect}, priority ${r.priority}, cost ${r.cost}): decides ${r.decidesRequests} request(s), ` +
        `${r.propertyRelevant} property-relevant, ${r.propertyViolations} violation(s) -> ${verdict}`,
    );
  }
  lines.push("");
  lines.push(
    report.propertyHolds
      ? `VERIFIED: property holds for all ${report.propertyRelevantRequests} relevant requests in the finite domain.`
      : `FAILED: ${report.violationsAfter} violation(s) remain, e.g. ${report.violatingRequests
          .map(formatRequest)
          .join(" | ")}`,
  );
  return lines.join("\n");
}
