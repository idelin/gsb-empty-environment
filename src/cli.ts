import * as fs from "fs";
import { solve } from "./solver";
import { SolverInput } from "./types";

function readInput(argv: string[]): string {
  const file = argv[2];
  if (file && file !== "-") return fs.readFileSync(file, "utf8");
  return fs.readFileSync(0, "utf8"); // stdin
}

function main(): void {
  let input: SolverInput;
  try {
    input = JSON.parse(readInput(process.argv)) as SolverInput;
  } catch (err) {
    console.error(`failed to read input: ${(err as Error).message}`);
    process.exit(2);
  }
  let result;
  try {
    result = solve(input);
  } catch (err) {
    console.error(`solver error: ${(err as Error).message}`);
    process.exit(2);
  }
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.status === "ok" ? 0 : 1);
}

main();
