import { World, type ScenarioReport } from "./world.ts";
import { scenarioAsyncTakeover } from "./scenario-async-takeover.ts";
import { scenarioCrossedInvitations } from "./scenario-crossed-invitations.ts";
import { scenarioRevocationLate } from "./scenario-revocation-late.ts";
import { scenarioLostResponse } from "./scenario-lost-response.ts";
import { scenarioTwoGroups } from "./scenario-two-groups.ts";

export interface ReferenceReport {
  readonly scenarios: readonly ScenarioReport[];
  readonly allPassed: boolean;
}

/**
 * Full two-node event-replay reference covering:
 * async takeover, crossed invitations, late confirmation after revocation,
 * receiver-received/relay-lost response, and one participant in two groups.
 */
export async function runReplayReference(): Promise<ReferenceReport> {
  const scenarios = [
    await scenarioAsyncTakeover(new World()),
    await scenarioCrossedInvitations(new World()),
    await scenarioRevocationLate(new World()),
    await scenarioLostResponse(new World()),
    await scenarioTwoGroups(new World()),
  ];
  return {
    scenarios,
    allPassed: scenarios.every((scenario) => scenario.checks.every((check) => check.pass)),
  };
}

export function formatReport(report: ReferenceReport): string {
  const lines: string[] = [];
  for (const scenario of report.scenarios) {
    lines.push(`\n## ${scenario.scenario} (${scenario.events.length} events)`);
    for (const check of scenario.checks) {
      lines.push(`  ${check.pass ? "PASS" : "FAIL"}  ${check.name}`);
      if (!check.pass) {
        lines.push(`        ${check.detail}`);
      }
    }
  }
  lines.push(`\nreference result: ${report.allPassed ? "ALL PASS" : "FAILURES PRESENT"}`);
  return lines.join("\n");
}
