import { readFileSync } from "node:fs";
import { analyzeBundle } from "./analyze.js";
import type { BundlerInput } from "./types.js";

declare const process: any;

function readInput(): string {
  const arg = process.argv[2];
  if (arg && arg !== "-") {
    return readFileSync(arg, "utf8");
  }
  return readFileSync(0, "utf8");
}

const input: BundlerInput = JSON.parse(readInput());
const report = analyzeBundle(input);
process.stdout.write(JSON.stringify(report, null, 2) + "\n");
if (!report.ok) {
  process.exitCode = 1;
}
