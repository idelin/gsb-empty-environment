#!/usr/bin/env node
// CLI: run named scenarios against a local directory, or inspect/truncate a log.
//
//   node src/cli.ts run <scenario> [--dir <path>]   run a scripted scenario
//   node src/cli.ts plan [--dir <path>]             print retention boundary + reasons
//   node src/cli.ts truncate [--dir <path>] [lsn]   reclaim segments (refuses if unsafe)
//   node src/cli.ts dump [--dir <path>]             dump retained log records
//   node src/cli.ts pages [--dir <path>]            print committed page versions

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { SCENARIOS, freshDir } from "./scenarios.ts";
import { System } from "./system.ts";

function parseArgs(argv: string[]): { cmd: string; rest: string[]; dir: string | null } {
  const [cmd = "help", ...rest] = argv;
  let dir: string | null = null;
  const positional: string[] = [];
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === "--dir") {
      dir = rest[++i];
    } else {
      positional.push(rest[i]);
    }
  }
  return { cmd, rest: positional, dir };
}

function defaultDir(name: string): string {
  return freshDir(fs.mkdtempSync(path.join(os.tmpdir(), "ptl-")), name);
}

function print(value: unknown): void {
  console.log(JSON.stringify(value, null, 2));
}

const { cmd, rest, dir } = parseArgs(process.argv.slice(2));

switch (cmd) {
  case "run": {
    const name = rest[0];
    const scenario = SCENARIOS[name];
    if (!scenario) {
      console.error(`unknown scenario "${name}". available: ${Object.keys(SCENARIOS).join(", ")}`);
      process.exit(1);
    }
    const workDir = dir ?? defaultDir(name);
    if (dir) freshDir(path.dirname(dir), path.basename(dir));
    const result = scenario(workDir);
    print({ dir: workDir, result });
    break;
  }
  case "plan": {
    const sys = System.open(dir ?? defaultDir("plan"));
    print(sys.planTruncation());
    break;
  }
  case "truncate": {
    const sys = System.open(dir ?? defaultDir("truncate"));
    const requested = rest[0] !== undefined ? Number(rest[0]) : undefined;
    try {
      print(sys.truncateLog(requested));
    } catch (e) {
      console.error((e as Error).message);
      process.exit(2);
    }
    break;
  }
  case "dump": {
    const sys = System.open(dir ?? defaultDir("dump"));
    for (const r of sys.log.all()) console.log(JSON.stringify(r));
    break;
  }
  case "pages": {
    const sys = System.open(dir ?? defaultDir("pages"));
    print(sys.pages.committedSnapshot());
    break;
  }
  default:
    console.error(`usage: node src/cli.ts <run <scenario>|plan|truncate [lsn]|dump|pages> [--dir <path>]`);
    console.error(`scenarios: ${Object.keys(SCENARIOS).join(", ")}`);
    process.exit(cmd === "help" ? 0 : 1);
}
