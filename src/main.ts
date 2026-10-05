import { readFileSync } from "node:fs";
import { InputError, solve } from "./solver.ts";

function readInput(args: string[]): string {
  if (args.length === 0 || args[0] === "-") {
    return readFileSync(0, "utf8");
  }
  return readFileSync(args[0], "utf8");
}

function main(): void {
  const args = process.argv.slice(2);
  if (args.includes("-h") || args.includes("--help")) {
    console.log("usage: node src/main.ts [input.json]   (reads stdin when no file is given)");
    return;
  }

  let input: unknown;
  try {
    input = JSON.parse(readInput(args));
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.log(JSON.stringify({ status: "error", message: `cannot read/parse input: ${message}` }, null, 2));
    process.exitCode = 1;
    return;
  }

  try {
    const result = solve(input);
    console.log(JSON.stringify(result, null, 2));
  } catch (err) {
    if (err instanceof InputError) {
      console.log(JSON.stringify({ status: "error", message: err.message }, null, 2));
      process.exitCode = 1;
      return;
    }
    throw err;
  }
}

main();
