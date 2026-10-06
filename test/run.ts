import { run as opaque } from "./opaque.test";
import { run as composite } from "./composite.test";
import { run as scene } from "./scene.test";

const suites: [string, () => void][] = [
  ["opaque", opaque],
  ["composite", composite],
  ["scene", scene],
];

let failed = 0;
for (const [name, fn] of suites) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (e) {
    failed++;
    console.error(`FAIL ${name}: ${(e as Error).message}`);
  }
}
if (failed > 0) {
  console.error(`${failed} suite(s) failed`);
  process.exit(1);
}
console.log("all suites passed");
