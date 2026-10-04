import { readFileSync } from "node:fs";
import { InputError, parseProblem } from "./model.ts";
import { solve } from "./solver.ts";

function readInput(argv: string[]): string {
  const fileArg = argv.find((arg) => !arg.startsWith("-"));
  if (fileArg !== undefined) {
    return readFileSync(fileArg, "utf8");
  }
  return readFileSync(0, "utf8");
}

function main(): void {
  let raw: unknown;
  try {
    raw = JSON.parse(readInput(process.argv.slice(2)));
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(
      `${JSON.stringify({ status: "error", message: `invalid JSON input: ${message}` })}\n`,
    );
    process.exit(1);
  }

  try {
    const problem = parseProblem(raw);
    const result = solve(problem);
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
  } catch (error) {
    if (error instanceof InputError) {
      process.stderr.write(
        `${JSON.stringify({ status: "error", message: error.message })}\n`,
      );
      process.exit(1);
    }
    throw error;
  }
}

main();
