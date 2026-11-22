import { formatReport, runReplayReference } from "./reference.ts";

const report = await runReplayReference();
console.log(formatReport(report));
if (!report.allPassed) {
  process.exitCode = 1;
}
