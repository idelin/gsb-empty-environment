import { tests as utf8Tests } from "./utf8.test";
import { tests as editorTests } from "./editor.test";
import { tests as fuzzTests } from "./fuzz.test";

const suites: Array<[string, Array<[string, () => void]>]> = [
  ["utf8", utf8Tests],
  ["editor", editorTests],
  ["fuzz", fuzzTests],
];

let passed = 0;
let failed = 0;
for (const [suite, tests] of suites) {
  for (const [name, fn] of tests) {
    try {
      fn();
      passed++;
      console.log("ok   " + suite + " / " + name);
    } catch (e) {
      failed++;
      console.error("FAIL " + suite + " / " + name);
      console.error(e instanceof Error ? e.stack : e);
    }
  }
}
console.log("");
console.log(passed + " passed, " + failed + " failed");
if (failed > 0) process.exit(1);
