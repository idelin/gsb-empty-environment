#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { AllocationError, DEFAULT_BUDGET, solve } from "./allocator.ts";
import { solveExhaustive } from "./exhaustive.ts";
import { generateInstance } from "./synth.ts";
import type { AllocationResult, Problem } from "./types.ts";

const USAGE = `Region-aware offline request allocator.

Usage:
  node src/cli.ts solve <input.json | -> [--budget N] [--exhaustive]
  node src/cli.ts demo [--seed S] [--requests R] [--nodes N] [--regions G]
  node src/cli.ts help

Input JSON:
  {
    "requests":  [{ "id": "r1", "region": "east", "demand": 4 }, ...],
    "nodes":     [{ "id": "n1", "region": "east", "capacity": 10 }, ...],
    "costMatrix": { "east": { "east": 0, "west": 5 }, "west": { ... } },
    "previous":  { "r1": "n1" },            // optional previous plan
    "budget":    100000                     // optional, --budget overrides
  }

Objectives (lexicographic): maximize admitted demand, then minimize
cross-region cost, then minimize migrations vs "previous", then minimize
the node-id vector ordered by request id (rejected sorts last).

The result is always feasible (no node over capacity). "optimal": false
means the search budget ran out before optimality could be proven.
`;

interface ParsedArgs {
  positional: string[];
  flags: Map<string, string>;
}

function parseArgs(argv: string[]): ParsedArgs {
  const positional: string[] = [];
  const flags = new Map<string, string>();
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg.startsWith("--")) {
      const eq = arg.indexOf("=");
      if (eq >= 0) {
        flags.set(arg.slice(2, eq), arg.slice(eq + 1));
      } else if (i + 1 < argv.length && !argv[i + 1].startsWith("--")) {
        flags.set(arg.slice(2), argv[++i]);
      } else {
        flags.set(arg.slice(2), "true");
      }
    } else {
      positional.push(arg);
    }
  }
  return { positional, flags };
}

function parseIntFlag(flags: Map<string, string>, name: string): number | undefined {
  const raw = flags.get(name);
  if (raw === undefined) return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new AllocationError(`--${name} must be a non-negative integer, got ${raw}`);
  }
  return value;
}

function readProblem(path: string): Problem & { budget?: number } {
  const text = path === "-" ? readFileSync(0, "utf8") : readFileSync(path, "utf8");
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new AllocationError(`invalid JSON in ${path}: ${(error as Error).message}`);
  }
}

function objectiveView(result: AllocationResult) {
  return {
    assignment: result.assignment,
    admittedDemand: result.stats.admittedDemand,
    crossRegionCost: result.stats.crossRegionCost,
    migratedCount: result.stats.migratedCount,
  };
}

function runSolve(argv: string[]): number {
  const { positional, flags } = parseArgs(argv);
  const input = positional[0];
  if (!input) throw new AllocationError("solve requires an input file (or - for stdin)");
  const spec = readProblem(input);
  const { budget: specBudget, ...problem } = spec;
  const budget = parseIntFlag(flags, "budget") ?? specBudget ?? DEFAULT_BUDGET;

  const result = solve(problem, { budget });
  const output: Record<string, unknown> = { ...result };

  if (flags.has("exhaustive")) {
    try {
      const reference = solveExhaustive(problem);
      const matches =
        JSON.stringify(objectiveView(result)) === JSON.stringify(objectiveView(reference));
      output.verification = {
        checked: true,
        matches,
        exhaustiveOptimal: objectiveView(reference),
      };
      if (!matches) {
        process.stdout.write(JSON.stringify(output, null, 2) + "\n");
        process.stderr.write("ERROR: bounded search disagrees with exhaustive search\n");
        return 2;
      }
    } catch (error) {
      if (error instanceof AllocationError) {
        output.verification = { checked: false, reason: error.message };
      } else {
        throw error;
      }
    }
  }

  process.stdout.write(JSON.stringify(output, null, 2) + "\n");
  return 0;
}

function runDemo(argv: string[]): number {
  const { flags } = parseArgs(argv);
  const seed = parseIntFlag(flags, "seed") ?? 7;
  const requestCount = parseIntFlag(flags, "requests") ?? 8;
  const nodeCount = parseIntFlag(flags, "nodes") ?? 3;
  const regionCount = parseIntFlag(flags, "regions") ?? 2;
  const problem = generateInstance(seed, {
    requestCount,
    nodeCount,
    regionCount,
    withPrevious: true,
  });
  process.stdout.write(JSON.stringify(problem, null, 2) + "\n");
  return 0;
}

function main(argv: string[]): number {
  const command = argv[0];
  switch (command) {
    case "solve":
      return runSolve(argv.slice(1));
    case "demo":
      return runDemo(argv.slice(1));
    case "help":
    case "--help":
    case "-h":
    case undefined:
      process.stdout.write(USAGE);
      return command === undefined ? 0 : 0;
    default:
      process.stderr.write(`unknown command: ${command}\n\n${USAGE}`);
      return 1;
  }
}

try {
  process.exitCode = main(process.argv.slice(2));
} catch (error) {
  if (error instanceof AllocationError) {
    process.stderr.write(`error: ${error.message}\n`);
    process.exitCode = 1;
  } else {
    throw error;
  }
}
