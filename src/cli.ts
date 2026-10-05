import { readFileSync } from "node:fs";
import { checkApprovals } from "./checker.ts";
import type { CheckerInput } from "./types.ts";

function readInput(argv: string[]): string {
  const fileArg = argv.find((arg) => !arg.startsWith("-"));
  if (fileArg !== undefined) return readFileSync(fileArg, "utf8");
  return readFileSync(0, "utf8");
}

function main(): void {
  let input: CheckerInput;
  try {
    input = JSON.parse(readInput(process.argv.slice(2))) as CheckerInput;
  } catch (error) {
    console.error(`failed to read input JSON: ${(error as Error).message}`);
    process.exit(2);
  }
  try {
    const output = checkApprovals(input);
    console.log(JSON.stringify(output, null, 2));
    process.exit(output.ok ? 0 : 1);
  } catch (error) {
    console.error((error as Error).message);
    process.exit(2);
  }
}

main();
