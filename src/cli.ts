/**
 * CLI:
 *   node src/cli.ts solve <problem.json> [--budget N] [--cert out.json]
 *   node src/cli.ts verify <problem.json> <cert.json>
 *   node src/cli.ts exhaustive <problem.json> [--cap N]
 *
 * All numbers are exact rationals (BigInt-backed). Display uses round-half-even
 * at 6 decimal places; exact canonical fractions are printed alongside.
 */

import { readFileSync, writeFileSync } from "node:fs";
import * as R from "./rational.ts";
import { analyze, parseProblem, totalCost } from "./model.ts";
import type { ProblemSpec } from "./model.ts";
import { solve } from "./solver.ts";
import { exhaustive } from "./exhaustive.ts";
import { buildCertificate, verifyCertificate } from "./certificate.ts";

function fmt(x: R.Rational): string {
  const dec = R.toDecimal(x, 6, "half-even");
  const can = R.toCanonical(x);
  return can.includes("/") ? `${dec} (${can})` : dec;
}

function loadProblem(path: string) {
  const spec = JSON.parse(readFileSync(path, "utf8")) as ProblemSpec;
  return parseProblem(spec);
}

function printBreakdown(problem: ReturnType<typeof loadProblem>, assignment: Map<number, number>) {
  const a = analyze(problem, assignment);
  console.log("Per-component contributions (sensitivity-scaled error intervals):");
  for (const c of a.perComponent) {
    for (const s of c.perSource) {
      console.log(
        `  ${c.component}[${c.grade}] x ${s.source}: [${fmt(s.interval.lo)}, ${fmt(s.interval.hi)}]`,
      );
    }
  }
  console.log("Per-source totals (correlated within source):");
  for (const s of a.perSource) {
    console.log(`  ${s.source}: [${fmt(s.interval.lo)}, ${fmt(s.interval.hi)}]`);
  }
  console.log(`Total error interval: [${fmt(a.total.lo)}, ${fmt(a.total.hi)}]`);
  console.log(
    `Worst-case |error| = ${fmt(a.worst)}  vs limit ${fmt(problem.errorLimit)}  => ${a.feasible ? "FEASIBLE" : "INFEASIBLE"}`,
  );
  console.log(`Total cost: ${fmt(totalCost(problem, assignment))}`);
}

function main(argv: string[]): number {
  const [cmd, ...rest] = argv;
  const positional = rest.filter((a) => !a.startsWith("--"));
  const opt = (name: string): string | undefined => {
    const i = rest.indexOf(`--${name}`);
    return i === -1 ? undefined : rest[i + 1];
  };

  if (cmd === "solve") {
    const problem = loadProblem(positional[0]);
    const maxNodes = opt("budget") ? Number(opt("budget")) : undefined;
    if (maxNodes !== undefined && (!Number.isSafeInteger(maxNodes) || maxNodes < 1)) {
      console.error("--budget must be a positive safe integer");
      return 2;
    }
    const budget = maxNodes ?? 1_000_000;
    const result = solve(problem, { maxNodes: budget });
    console.log(`Status: ${result.status}${result.budgetExhausted ? " (node budget exhausted — optimality NOT established)" : ""}`);
    console.log(`Nodes explored: ${result.nodesExplored}`);
    if (result.upperBound !== null) console.log(`Feasible upper bound (best cost): ${fmt(result.upperBound)}`);
    if (result.lowerBound !== null) console.log(`Proven lower bound: ${fmt(result.lowerBound)}`);
    if (result.bestAssignment) {
      console.log("");
      printBreakdown(problem, result.bestAssignment);
    }
    const certPath = opt("cert");
    if (certPath) {
      const cert = buildCertificate(problem, result, budget);
      writeFileSync(certPath, JSON.stringify(cert, null, 2) + "\n");
      console.log(`\nCertificate written to ${certPath}`);
    }
    return result.status === "infeasible" ? 3 : 0;
  }

  if (cmd === "verify") {
    const problem = loadProblem(positional[0]);
    const cert = JSON.parse(readFileSync(positional[1], "utf8"));
    const v = verifyCertificate(problem, cert);
    console.log(`Certificate valid: ${v.valid}${v.stale ? " (STALE: problem/objective changed since issuance)" : ""}`);
    for (const r of v.reasons) console.log(`  - ${r}`);
    return v.valid ? 0 : 1;
  }

  if (cmd === "exhaustive") {
    const problem = loadProblem(positional[0]);
    const cap = opt("cap") ? Number(opt("cap")) : undefined;
    const r = exhaustive(problem, cap);
    if (r.status === "too-large") {
      console.log("Instance too large for exhaustive check (raise --cap to force).");
      return 4;
    }
    console.log(`Combinations checked: ${r.combinationsChecked}, feasible: ${r.feasibleCount}`);
    if (r.bestCost !== null) {
      console.log(`Exhaustive optimum cost: ${fmt(r.bestCost)}`);
      printBreakdown(problem, r.bestAssignment!);
    } else {
      console.log("No feasible combination exists.");
    }
    return 0;
  }

  console.error("usage: solve|verify|exhaustive ...");
  return 2;
}

process.exit(main(process.argv.slice(2)));
