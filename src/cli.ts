import * as path from "path";
import { audit } from "./auditor";
import { BuildSpec } from "./model";
import { formatReport } from "./report";

function usage(): never {
  console.error("usage: auditor <build-spec-module.js> [--json] [--max-environments N]");
  process.exit(2);
}

function main(argv: string[]): void {
  const args = argv.slice(2);
  if (args.length === 0) usage();
  const json = args.includes("--json");
  const positional = args.filter((a) => !a.startsWith("--"));
  const maxEnvIndex = args.indexOf("--max-environments");
  const maxEnvironments =
    maxEnvIndex >= 0 ? Number(args[maxEnvIndex + 1]) : undefined;
  const specPath = positional[0];
  if (!specPath) usage();

  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const mod = require(path.resolve(specPath));
  const spec: BuildSpec = mod.spec ?? mod.default ?? mod;
  if (!spec.domain || !spec.build || !spec.keyInputs) {
    console.error(`Module ${specPath} does not export a BuildSpec (expected export "spec").`);
    process.exit(2);
  }

  const report = audit(spec, { maxEnvironments });
  if (json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log(formatReport(report));
  }
  process.exit(report.counterexamples.some((c) => c.kind === "artifact-affecting") ? 1 : 0);
}

main(process.argv);
