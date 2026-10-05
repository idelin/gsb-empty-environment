import { readFileSync } from "node:fs";
import { plan } from "./planner.ts";
import { formatReport } from "./report.ts";
import type { ProblemSpec } from "./types.ts";

function usage(): never {
  console.error("用法:");
  console.error("  node src/cli.ts <spec.json> [--json]");
  console.error("  node src/cli.ts --demo [--json]");
  process.exit(2);
}

const args = process.argv.slice(2);
const asJson = args.includes("--json");
const rest = args.filter((a) => a !== "--json");

let specPath: string;
if (rest[0] === "--demo") {
  specPath = new URL("../examples/demo.json", import.meta.url).pathname;
} else if (rest.length === 1) {
  specPath = rest[0];
} else {
  usage();
}

let spec: ProblemSpec;
try {
  spec = JSON.parse(readFileSync(specPath, "utf8"));
} catch (err) {
  console.error(`无法读取输入文件 ${specPath}: ${(err as Error).message}`);
  process.exit(2);
}

try {
  const analysis = plan(spec);
  if (asJson) {
    console.log(JSON.stringify(analysis, null, 2));
  } else {
    console.log(formatReport(analysis));
  }
} catch (err) {
  console.error((err as Error).message);
  process.exit(1);
}
