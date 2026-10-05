import { readdirSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
execFileSync(process.execPath, [join(root, "scripts/build.mjs")], { stdio: "inherit" });

const testDir = join(root, ".build/test");
const helpers = await import(pathToFileURL(join(testDir, "helpers.js")).href);
const names = readdirSync(testDir).filter((n) => n.endsWith(".test.js")).sort();
for (const name of names) {
  await import(pathToFileURL(join(testDir, name)).href);
}
const tests = helpers.getTests();
let failed = 0;
for (const t of tests) {
  try {
    t.fn();
    console.log("ok   " + t.name);
  } catch (err) {
    failed++;
    console.error("FAIL " + t.name);
    console.error(String(err && err.stack ? err.stack : err));
  }
}
console.log(tests.length + " tests, " + failed + " failed");
process.exitCode = failed === 0 ? 0 : 1;
