#!/usr/bin/env node
/**
 * 命令行：
 *   node src/cli.ts solve <problem.json | -> [--budget N]
 *   node src/cli.ts check <problem.json> <result.json>
 *
 * 问题格式（系数为整数或 "p/q" 字符串）：
 *   { "c": [...], "A": [[...], ...], "b": [...], "budget": 10000 }
 */

import { readFileSync } from "node:fs";
import { solve, DEFAULT_BUDGET } from "./simplex.ts";
import { checkResult } from "./checker.ts";
import { parseProblem, parseResult, formatResult } from "./parse.ts";

function readJson(path: string): unknown {
  const text = path === "-" ? readFileSync(0, "utf8") : readFileSync(path, "utf8");
  return JSON.parse(text);
}

function usage(): never {
  console.error("usage:");
  console.error("  node src/cli.ts solve <problem.json | -> [--budget N]");
  console.error("  node src/cli.ts check <problem.json> <result.json>");
  process.exit(2);
}

function main(argv: string[]): void {
  const [cmd, ...rest] = argv;
  if (cmd === "solve") {
    let budget: number | undefined;
    const files: string[] = [];
    for (let i = 0; i < rest.length; i++) {
      if (rest[i] === "--budget") {
        budget = Number(rest[++i]);
        if (!Number.isInteger(budget) || budget < 0) {
          throw new Error("--budget must be a non-negative integer");
        }
      } else {
        files.push(rest[i]);
      }
    }
    if (files.length > 1) usage();
    const problem = parseProblem(readJson(files[0] ?? "-"));
    const result = solve(problem, budget ?? problem.budget ?? DEFAULT_BUDGET);
    console.log(JSON.stringify(formatResult(result), null, 2));
    if (result.status === "iteration_limit") process.exitCode = 3;
  } else if (cmd === "check") {
    if (rest.length !== 2) usage();
    const problem = parseProblem(readJson(rest[0]));
    const result = parseResult(readJson(rest[1]));
    const check = checkResult(problem, result);
    console.log(JSON.stringify(check, null, 2));
    if (!check.ok) process.exitCode = 1;
  } else {
    usage();
  }
}

try {
  main(process.argv.slice(2));
} catch (err) {
  console.error(`error: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(2);
}
