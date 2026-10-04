import { readFileSync } from "fs";
import { solve } from "./solver";
import { buildReport, validatePolicyShape } from "./report";

function main(argv: string[]): number {
  const file = argv[2];
  if (!file) {
    console.error("usage: node dist/src/cli.js <policy.json>");
    return 2;
  }
  let policy;
  try {
    policy = validatePolicyShape(JSON.parse(readFileSync(file, "utf8")));
  } catch (err) {
    console.error(`invalid policy: ${(err as Error).message}`);
    return 2;
  }

  const result = solve(policy);
  if (result.status === "too-many-exceptions") {
    console.log(
      JSON.stringify(
        {
          status: "too-many-exceptions",
          count: result.count,
          limit: 24,
          message: `policy declares ${result.count} exceptions; at most 24 are supported`,
        },
        null,
        2
      )
    );
    return 1;
  }
  if (result.status === "infeasible") {
    console.log(JSON.stringify({ status: "infeasible", reason: result.reason }, null, 2));
    return 1;
  }

  console.log(JSON.stringify(buildReport(policy, result), null, 2));
  return 0;
}

process.exit(main(process.argv));
