#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { solve } from "./solver.ts";

function usage(): never {
  console.error("usage: node src/cli.ts <input.json | ->");
  process.exit(2);
}

const arg = process.argv[2];
if (!arg) usage();

let text: string;
try {
  text = arg === "-" ? readFileSync(0, "utf8") : readFileSync(arg, "utf8");
} catch (err) {
  console.error(`cannot read input: ${(err as Error).message}`);
  process.exit(2);
}

let instance: unknown;
try {
  instance = JSON.parse(text);
} catch (err) {
  console.error(`invalid JSON: ${(err as Error).message}`);
  process.exit(2);
}

try {
  const result = solve(instance as Parameters<typeof solve>[0]);
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.status === "unsatisfiable" ? 1 : 0);
} catch (err) {
  console.error((err as Error).message);
  process.exit(2);
}
