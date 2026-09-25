#!/usr/bin/env node
import { readFileSync } from "node:fs";
import {
  applyMoves,
  formatPath,
  JsonMoveError,
  planMoves,
  type JsonValue,
} from "./jsonmove.ts";

function usage(): never {
  console.error("usage: node src/cli.ts [--plan] <pattern> <replacement> [file]");
  console.error("  reads JSON from [file] or stdin, writes JSON to stdout");
  process.exit(2);
}

const args = process.argv.slice(2);
const dryRun = args.includes("--plan");
const positional = args.filter((a) => a !== "--plan");
if (positional.length < 2 || positional.length > 3) usage();
const [pattern, replacement, file] = positional;

let text: string;
try {
  text = file ? readFileSync(file, "utf8") : readFileSync(0, "utf8");
} catch (err) {
  console.error(`error: cannot read input: ${(err as Error).message}`);
  process.exit(2);
}

let data: JsonValue;
try {
  data = JSON.parse(text) as JsonValue;
} catch (err) {
  console.error(`error: invalid JSON: ${(err as Error).message}`);
  process.exit(2);
}

try {
  if (dryRun) {
    const plan = planMoves(data, pattern, replacement);
    console.log(
      JSON.stringify(
        plan.map((e) => ({
          from: formatPath(e.from),
          to: formatPath(e.to),
          noop: e.noop,
        })),
        null,
        2,
      ),
    );
  } else {
    console.log(JSON.stringify(applyMoves(data, pattern, replacement), null, 2));
  }
} catch (err) {
  if (err instanceof JsonMoveError) {
    console.error(`error: ${err.message}`);
    process.exit(1);
  }
  throw err;
}
