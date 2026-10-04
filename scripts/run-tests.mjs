import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { stripTypes } from "./strip-types.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const jobs = [
  ["src/normalize.ts", "build/src/normalize.mjs"],
  ["test/normalize.test.ts", "build/test/normalize.test.mjs"],
];
for (const [src, dst] of jobs) {
  const code = stripTypes(readFileSync(join(root, src), "utf8"));
  const out = join(root, dst);
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, code);
}
await import("../build/test/normalize.test.mjs");
