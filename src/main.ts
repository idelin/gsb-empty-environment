import { readFileSync } from "node:fs";
import { simulate } from "./simulator.ts";
import { formatResult } from "./format.ts";
import { validateScenario } from "./validate.ts";

function main(): void {
  const file = process.argv[2];
  if (!file) {
    console.error("usage: node src/main.ts <scenario.json>");
    process.exitCode = 1;
    return;
  }
  let raw: unknown;
  try {
    raw = JSON.parse(readFileSync(file, "utf8"));
  } catch (err) {
    console.error(`cannot read/parse ${file}: ${(err as Error).message}`);
    process.exitCode = 1;
    return;
  }
  try {
    const scenario = validateScenario(raw);
    const result = simulate(scenario);
    console.log(formatResult(result));
    if (result.status === "deadlock") process.exitCode = 2;
    if (result.status === "max-ticks-exceeded") process.exitCode = 3;
  } catch (err) {
    console.error((err as Error).message);
    process.exitCode = 1;
  }
}

main();
