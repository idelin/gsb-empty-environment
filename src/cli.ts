#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { analyze } from "./analyzer.ts";
import type { AnalysisResult } from "./analyzer.ts";
import { parseGraph } from "./parser.ts";

function usage(): never {
  console.error("usage: node --experimental-strip-types src/cli.ts <graph.json> [--json]");
  process.exit(2);
}

function renderHuman(result: AnalysisResult): string {
  const lines: string[] = [];
  if (!result.found) {
    lines.push("NO FLOW: no feasible tainted path from any source to a dangerous call.");
    if (result.truncated) lines.push("(search truncated: state budget exhausted)");
    lines.push(`explored states: ${result.exploredStates}`);
    return lines.join("\n");
  }
  const sink = result.sinkLabel ?? result.sinkId;
  lines.push(
    `TAINT FLOW: {${(result.taintAtSink ?? []).join(", ")}} reaches dangerous call "${sink}"`,
  );
  const path = result.path ?? [];
  lines.push(`shortest feasible path (${path.length} nodes):`);
  for (const step of path) {
    const via =
      step.viaCondition !== undefined
        ? `  [when ${step.viaCondition.var} == ${step.viaCondition.value}]`
        : "";
    lines.push(`  ${step.nodeId}  <${step.kind}>  ${step.description}${via}`);
  }
  const constraints = Object.entries(result.constraints ?? {});
  if (constraints.length > 0) {
    lines.push(
      `path constraints: ${constraints.map(([k, v]) => `${k} = ${v}`).join(", ")}`,
    );
  }
  lines.push(`explored states: ${result.exploredStates}`);
  return lines.join("\n");
}

function main(): void {
  const args = process.argv.slice(2);
  const asJson = args.includes("--json");
  const file = args.find((arg) => !arg.startsWith("--"));
  if (file === undefined) usage();

  let raw: unknown;
  try {
    raw = JSON.parse(readFileSync(file, "utf8"));
  } catch (error) {
    console.error(`cannot read/parse ${file}: ${(error as Error).message}`);
    process.exit(2);
  }

  let result: AnalysisResult;
  try {
    result = analyze(parseGraph(raw));
  } catch (error) {
    console.error((error as Error).message);
    process.exit(2);
  }

  console.log(asJson ? JSON.stringify(result, null, 2) : renderHuman(result));
  // Exit code 1 signals a found taint flow (useful in CI gates).
  process.exit(result.found ? 1 : 0);
}

main();
