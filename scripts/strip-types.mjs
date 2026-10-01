// Zero-dependency TypeScript -> JavaScript build step.
//
// This repository is written in a restricted, strip-friendly subset of
// TypeScript so it can be built and tested without any npm dependencies:
//   - type annotations use simple named types only (primitives, Uint8Array,
//     or PascalCase names), optionally with a single generic argument or []
//   - interfaces are single-level blocks, type aliases are single-line
//   - no enums, no parameter properties, no `as` casts, no `!` assertions
// The output is plain ESM JavaScript in dist/, verified with `node --check`.
// With a real toolchain available, `tsc -p tsconfig.json` works as well.

import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { execFileSync } from "node:child_process";

const ROOT = new URL("..", import.meta.url).pathname;
const SRC_DIRS = ["src", "test", "examples"];
const OUT = join(ROOT, "dist");

const TYPE =
  "(?:bigint|number|string|boolean|void|unknown|any|Uint8Array|Int32Array" +
  "|[A-Z][A-Za-z0-9_]*)(?:<[^<>]*>)?(?:\\[\\])*";

function stripTypes(src) {
  return src
    .replace(/^[ \t]*declare\s.*$/gm, "")
    .replace(/^[ \t]*(?:export\s+)?interface\s+\w+\s*\{[^{}]*\}\s*$/gm, "")
    .replace(/^[ \t]*(?:export\s+)?type\s+\w+\s*=[^;]*;\s*$/gm, "")
    .replace(/^[ \t]*import\s+type\s[^\n]*\n/gm, "")
    .replace(/\b(?:private|public|protected|readonly|abstract)\s+/g, "")
    .replace(new RegExp(":\\s*" + TYPE + "(?=\\s*(?:[,)=;{]|=>|$))", "gm"), "");
}

rmSync(OUT, { recursive: true, force: true });
let count = 0;
for (const dir of SRC_DIRS) {
  const srcDir = join(ROOT, dir);
  const outDir = join(OUT, dir);
  mkdirSync(outDir, { recursive: true });
  for (const file of readdirSync(srcDir)) {
    if (!file.endsWith(".ts")) continue;
    const stripped = stripTypes(readFileSync(join(srcDir, file), "utf8"));
    const outFile = join(outDir, file.replace(/\.ts$/, ".js"));
    writeFileSync(outFile, stripped);
    execFileSync(process.execPath, ["--check", outFile]); // syntax-verify output
    count += 1;
  }
}
console.log(`build: stripped types from ${count} file(s) -> dist/`);
