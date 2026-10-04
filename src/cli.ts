#!/usr/bin/env node
/**
 * CLI:
 *   node dist/src/cli.js solve <problem.json|-> [--budget N]
 *   node dist/src/cli.js check <problem.json> <result.json|->
 */
import * as fs from "fs";
import { solve } from "./simplex";
import { checkResult } from "./checker";
import { parseLP, resultToJson, jsonToResult } from "./lpio";

function readJson(path: string): unknown {
  const text = path === "-" ? fs.readFileSync(0, "utf8") : fs.readFileSync(path, "utf8");
  return JSON.parse(text);
}

function fail(msg: string): never {
  console.error(`error: ${msg}`);
  process.exit(2);
}

function main(): void {
  const [cmd, ...args] = process.argv.slice(2);

  if (cmd === "solve") {
    const file = args[0];
    if (!file) fail("usage: cli.js solve <problem.json|-> [--budget N]");
    let budget: number | undefined;
    const bi = args.indexOf("--budget");
    if (bi >= 0) {
      budget = parseInt(args[bi + 1], 10);
      if (!Number.isSafeInteger(budget) || budget < 0) fail("invalid --budget");
    }
    const raw = readJson(file);
    const lp = parseLP(raw);
    if (budget === undefined && typeof (raw as { budget?: unknown }).budget === "number")
      budget = (raw as { budget: number }).budget;
    const res = budget === undefined ? solve(lp) : solve(lp, budget);
    console.log(JSON.stringify(resultToJson(res), null, 2));
    return;
  }

  if (cmd === "check") {
    const [probFile, resFile] = args;
    if (!probFile || !resFile) fail("usage: cli.js check <problem.json> <result.json|->");
    const lp = parseLP(readJson(probFile));
    const res = jsonToResult(readJson(resFile));
    const outcome = checkResult(lp, res);
    if (outcome.ok) {
      console.log("OK: certificate verified");
      return;
    }
    console.error("INVALID certificate:");
    for (const e of outcome.errors) console.error(`  - ${e}`);
    process.exit(1);
  }

  fail("usage: cli.js <solve|check> ...");
}

main();
