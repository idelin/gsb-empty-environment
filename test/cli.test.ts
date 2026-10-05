import { spawnSync } from "node:child_process";
import { test, assertEqual, assertTrue } from "./helpers.js";

test("cli: reads JSON file and prints report", () => {
  const res = spawnSync(process.execPath, [".build/src/cli.js", "examples/input.json"], { encoding: "utf8" });
  const report = JSON.parse(res.stdout);
  assertEqual(report.totalCost, 3);
  assertTrue(report.renames.length === 2, "expected two renames");
  const ref = report.references[0];
  assertEqual(ref.status, "updated");
  assertEqual(ref.newSpecifier, "./foo_2.js");
  assertEqual(report.references[1].status, "ambiguous");
  assertEqual(report.ok, false);
  assertEqual(res.status, 1);
});
