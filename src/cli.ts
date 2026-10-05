/**
 * CLI: node dist/src/cli.js analyze <graph.json> [--max-states N]
 *
 * Prints the shortest feasible path from a source to a dangerous call,
 * or reports that no feasible tainted path exists.
 */

import { readFileSync } from "fs";
import { analyze, TaintWitness } from "./analyzer";
import { parseGraph } from "./model";

function usage(): never {
  process.stderr.write(
    "usage: taint analyze <graph.json> [--max-states N]\n"
  );
  process.exitCode = 2;
  throw new Error("usage");
}

function formatWitness(w: TaintWitness): string {
  const out: string[] = [];
  if (!w.found) {
    out.push("RESULT: no feasible tainted path from any source to a dangerous call");
    out.push(`states explored: ${w.explored}`);
    return out.join("\n");
  }
  out.push(`RESULT: dangerous call reached at node "${w.sinkNodeId as string}"`);
  for (const d of w.dangers) {
    out.push(
      `  tainted argument: ${d.arg} (type ${d.type ?? "unknown"}) <- sources [${d.sources.join(", ")}]`
    );
  }
  out.push(`shortest feasible path (${w.path.length} nodes):`);
  for (const step of w.path) {
    const via = step.viaCond !== undefined ? `  --[${step.viaCond}]-->` : "";
    out.push(`  ${step.nodeId}${via}`);
    for (const note of step.notes) out.push(`      note: ${note}`);
  }
  out.push(`states explored: ${w.explored}`);
  return out.join("\n");
}

function main(argv: string[]): void {
  const args = argv.slice(2);
  if (args[0] !== "analyze") usage();
  const rest = args.slice(1);
  let file: string | undefined;
  let maxStates: number | undefined;
  for (let i = 0; i < rest.length; i++) {
    const a = rest[i] as string;
    if (a === "--max-states") {
      const v = rest[++i];
      if (v === undefined || !/^\d+$/.test(v)) usage();
      maxStates = Number(v);
    } else if (file === undefined) {
      file = a;
    } else {
      usage();
    }
  }
  if (file === undefined) usage();

  const raw = JSON.parse(readFileSync(file as string, "utf8"));
  const graph = parseGraph(raw);
  const witness = analyze(graph, maxStates !== undefined ? { maxStates } : {});
  process.stdout.write(formatWitness(witness) + "\n");
  if (witness.warnings.length > 0) {
    for (const w of new Set(witness.warnings)) {
      process.stderr.write(`warning: ${w}\n`);
    }
  }
  process.exitCode = witness.found ? 0 : 1;
}

try {
  main(process.argv);
} catch (err) {
  if (err instanceof Error && err.message === "usage") {
    // already reported
  } else {
    process.stderr.write(`error: ${(err as Error).message}\n`);
    process.exitCode = 2;
  }
}
