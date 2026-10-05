import { readFileSync } from "node:fs";
import { solve } from "./solver.ts";
import { InvalidInputError } from "./types.ts";
import type { Problem } from "./types.ts";

function main(): number {
  const arg = process.argv[2];
  let raw: string;
  try {
    raw = arg ? readFileSync(arg, "utf8") : readFileSync(0, "utf8");
  } catch {
    process.stderr.write(`cannot read input ${arg ?? "<stdin>"}\n`);
    return 2;
  }
  let problem: Problem;
  try {
    problem = JSON.parse(raw) as Problem;
  } catch {
    process.stderr.write("input is not valid JSON\n");
    return 2;
  }
  try {
    const result = solve(problem);
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    return result.ok ? 0 : 1;
  } catch (err) {
    if (err instanceof InvalidInputError) {
      process.stderr.write(`invalid input: ${err.message}\n`);
      return 2;
    }
    throw err;
  }
}

process.exit(main());
