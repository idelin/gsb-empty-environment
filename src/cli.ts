#!/usr/bin/env node
import * as fs from "fs";
import { reduceScript, ReductionError } from "./reducer";
import { ReplayLog } from "./replay-log";
import { SimulatedCluster } from "./simulator";
import { ScriptFile, Step } from "./types";
import { validateScript } from "./validate";

const RECOVERY_MS = 150;
const DEFAULT_NODES = ["A", "B", "C"];

interface Flags {
  [key: string]: string;
}

function parseArgs(argv: string[]): { command: string; flags: Flags } {
  const [command = "", ...rest] = argv;
  const flags: Flags = {};
  for (let i = 0; i < rest.length; i++) {
    const arg = rest[i];
    if (arg.startsWith("--")) {
      const key = arg.slice(2);
      const next = rest[i + 1];
      if (next !== undefined && !next.startsWith("--")) {
        flags[key] = next;
        i++;
      } else {
        flags[key] = "true";
      }
    }
  }
  return { command, flags };
}

function loadScript(path: string): ScriptFile {
  const parsed = JSON.parse(fs.readFileSync(path, "utf8"));
  const steps: Step[] = Array.isArray(parsed) ? parsed : parsed.steps;
  if (!Array.isArray(steps)) {
    throw new Error(`script file ${path} has no "steps" array`);
  }
  return {
    name: parsed.name,
    nodes: parsed.nodes,
    seed: parsed.seed,
    steps,
  };
}

function usage(): void {
  console.error(
    [
      "Usage:",
      "  psr reduce   --script <file> [--out reduced.json] [--log replays.jsonl] [--seed N] [--max-replays N]",
      "  psr replay   --script <file> [--seed N]",
      "  psr validate --script <file>",
    ].join("\n")
  );
}

function main(): number {
  const { command, flags } = parseArgs(process.argv.slice(2));
  if (!command || !flags.script) {
    usage();
    return 1;
  }

  const script = loadScript(flags.script);
  const nodes = script.nodes ?? DEFAULT_NODES;
  const seed = flags.seed !== undefined ? Number(flags.seed) : script.seed ?? 42;
  const harness = new SimulatedCluster({ nodes, seed });
  const validate = (steps: Step[]) => validateScript(steps, { nodes, recoveryMs: RECOVERY_MS });

  switch (command) {
    case "validate": {
      const result = validate(script.steps);
      if (result.ok) {
        console.log("valid");
        return 0;
      }
      console.error("invalid:");
      for (const e of result.errors) console.error(`  - ${e}`);
      return 1;
    }
    case "replay": {
      const validation = validate(script.steps);
      const summary = validation.ok
        ? harness.replay(script.steps)
        : {
            status: "invalid" as const,
            invalidReason: validation.errors.join("; "),
            simulatedMs: 0,
            events: 0,
            finalLeaders: [],
            finalTerms: {},
          };
      console.log(JSON.stringify(summary, null, 2));
      return summary.status === "fail" ? 0 : 1;
    }
    case "reduce": {
      const logPath = flags.log ?? "replays.jsonl";
      const outPath = flags.out ?? "reduced.json";
      const log = new ReplayLog(logPath);
      try {
        const report = reduceScript(script.steps, {
          harness,
          validate,
          log,
          maxReplays: flags["max-replays"] !== undefined ? Number(flags["max-replays"]) : undefined,
        });
        const reduced: ScriptFile = { ...script, seed, steps: report.reduced };
        fs.writeFileSync(outPath, JSON.stringify(reduced, null, 2) + "\n");
        console.log(`original:  ${report.originalLength} steps`);
        console.log(`reduced:   ${report.reducedLength} steps -> ${outPath}`);
        console.log(
          `replays:   ${report.totalReplays} ` +
            `(fail=${report.counts.fail}, pass=${report.counts.pass}, invalid=${report.counts.invalid})`
        );
        console.log(`replay log: ${logPath}`);
        console.log(
          report.verifiedOneMinimal
            ? "1-minimal: verified — no single-step deletion reproduces the failure"
            : "WARNING: result is not 1-minimal"
        );
        return report.verifiedOneMinimal ? 0 : 1;
      } catch (err) {
        if (err instanceof ReductionError) {
          console.error(`reduction failed: ${err.message}`);
          return 2;
        }
        throw err;
      }
    }
    default:
      usage();
      return 1;
  }
}

process.exit(main());
