/**
 * CLI entry: revoke-solve [scenario.json] [--check] [--json]
 *
 * Solves the minimum-cost exception revocation problem for a scenario and
 * verifies the result over the entire finite input domain.
 */

import { readFileSync } from "node:fs";
import { validateScenario } from "./model.ts";
import type { Scenario } from "./model.ts";
import { demoScenario } from "./scenario.ts";
import { formatRequest, solveExact, solveFast } from "./solver.ts";
import { renderReport, verifyPlan } from "./verify.ts";

interface CliArgs {
  scenarioPath: string | null;
  check: boolean;
  json: boolean;
}

function parseArgs(argv: string[]): CliArgs {
  const args: CliArgs = { scenarioPath: null, check: false, json: false };
  for (const arg of argv) {
    if (arg === "--check") args.check = true;
    else if (arg === "--json") args.json = true;
    else if (arg === "--help" || arg === "-h") {
      console.log("usage: node src/cli.ts [scenario.json] [--check] [--json]");
      process.exit(0);
    } else if (arg.startsWith("--")) {
      throw new Error(`unknown flag: ${arg}`);
    } else {
      args.scenarioPath = arg;
    }
  }
  return args;
}

function main(): void {
  const args = parseArgs(process.argv.slice(2));
  const scenario: Scenario = args.scenarioPath
    ? (JSON.parse(readFileSync(args.scenarioPath, "utf8")) as Scenario)
    : demoScenario;
  validateScenario(scenario);

  const outcome = solveFast(scenario);
  if (outcome.status !== "optimal") {
    console.error(`INFEASIBLE: ${outcome.reason}`);
    if (outcome.request) console.error(`  request: ${formatRequest(outcome.request)}`);
    process.exit(2);
  }

  const report = verifyPlan(scenario, outcome);

  if (args.json) {
    console.log(JSON.stringify({ plan: { ...outcome, witnesses: [...outcome.witnesses.keys()] }, report }, null, 2));
  } else {
    console.log(renderReport(scenario, outcome, report));
  }

  if (args.check) {
    const cross = solveExact(scenario);
    if (cross.status === "optimal") {
      const ok = cross.totalCost === outcome.totalCost;
      console.log(
        `Cross-check (branch-and-bound, ${cross.stats.nodesExplored} nodes): optimal cost ${cross.totalCost} ` +
          (ok ? "== fast solver result, CONFIRMED" : `!= fast solver result ${outcome.totalCost}, MISMATCH`),
      );
      if (!ok) process.exit(3);
    } else {
      console.log(`Cross-check: ${cross.status} (${cross.reason})`);
      if (cross.status !== "unknown") process.exit(3);
    }
  }

  if (!report.propertyHolds) process.exit(1);
}

main();
