#!/usr/bin/env node
// Dev-only type stripper.
//
// This environment has no TypeScript compiler available, so this script
// removes the small, deliberately restricted TypeScript subset used in
// src/ and test/ (type annotations, `import type`, `export type`,
// interfaces) and emits runnable ESM into .build/. It is NOT a
// general-purpose transpiler; with network access prefer the real thing:
//   npm install --save-dev typescript && npx tsc -p tsconfig.json
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const outRoot = join(root, ".build");
const inputDirs = ["src", "test"];

const KEYWORD_TYPE = /^(?:number|string|boolean|void|unknown|never|any)\b/;

function startsType(rest) {
  return /^[A-Z]/.test(rest) || KEYWORD_TYPE.test(rest);
}

function stripTypes(src, file) {
  let out = "";
  let i = 0;
  const n = src.length;
  let atLineStart = true;

  function fail(msg) {
    const line = src.slice(0, i).split("\n").length;
    throw new Error(file + ":" + line + ": " + msg);
  }

  while (i < n) {
    const ch = src[i];

    if (ch === "\n") {
      out += ch;
      i += 1;
      atLineStart = true;
      continue;
    }

    if (atLineStart) {
      if (ch === " " || ch === "\t") {
        out += ch;
        i += 1;
        continue;
      }
      const rest = src.slice(i);
      // `import type { ... } from "...";` / `export type ...;` (may span lines)
      if (/^(?:import\s+type\s|export\s+type\s)/.test(rest)) {
        let j = i;
        let depth = 0;
        while (j < n) {
          const c = src[j];
          if (c === "{" || c === "(" || c === "[") depth += 1;
          else if (c === "}" || c === ")" || c === "]") depth -= 1;
          else if (c === ";" && depth === 0) { j += 1; break; }
          j += 1;
        }
        if (j >= n) fail("unterminated type-only statement");
        i = j;
        continue;
      }
      // `interface Name { ... }` / `export interface Name { ... }`
      const im = rest.match(/^(?:export\s+)?interface\s+[A-Za-z_$][A-Za-z0-9_$]*[^{]*\{/);
      if (im) {
        let depth = 0;
        let j = i + im[0].length - 1; // at the opening brace
        while (j < n) {
          if (src[j] === "{") depth += 1;
          else if (src[j] === "}") {
            depth -= 1;
            if (depth === 0) break;
          }
          j += 1;
        }
        if (j >= n) fail("unterminated interface");
        i = j + 1;
        continue;
      }
      atLineStart = false;
      continue;
    }

    if (ch === "/" && src[i + 1] === "/") {
      const nl = src.indexOf("\n", i);
      if (nl === -1) { out += src.slice(i); i = n; } else { out += src.slice(i, nl); i = nl; }
      continue;
    }
    if (ch === "/" && src[i + 1] === "*") {
      const end = src.indexOf("*/", i + 2);
      if (end === -1) fail("unterminated block comment");
      out += src.slice(i, end + 2);
      i = end + 2;
      continue;
    }
    if (ch === '"' || ch === "'") {
      let j = i + 1;
      while (j < n && src[j] !== ch) {
        if (src[j] === "\\") j += 1;
        j += 1;
      }
      if (j >= n) fail("unterminated string");
      out += src.slice(i, j + 1);
      i = j + 1;
      continue;
    }
    if (ch === "`") {
      let j = i + 1;
      let depth = 0;
      let closed = false;
      while (j < n) {
        const c = src[j];
        if (c === "\\") { j += 2; continue; }
        if (c === "`") {
          if (depth === 0) { closed = true; break; }
          fail("nested template literals are not supported");
        }
        if (c === "$" && src[j + 1] === "{") { depth += 1; j += 2; continue; }
        if (c === "}" && depth > 0) { depth -= 1; j += 1; continue; }
        j += 1;
      }
      if (!closed) fail("unterminated template literal");
      out += src.slice(i, j + 1);
      i = j + 1;
      continue;
    }
    if (ch === ":") {
      let j = i + 1;
      while (j < n && (src[j] === " " || src[j] === "\t")) j += 1;
      if (startsType(src.slice(j))) {
        let k = j;
        let depth = 0;
        let done = false;
        while (k < n) {
          const c = src[k];
          if (c === "<" || c === "[") depth += 1;
          else if (c === ">" || c === "]") depth -= 1;
          else if (depth === 0 && (c === "," || c === ")" || c === "=" || c === "{" || c === ";" || c === "\n")) {
            done = true;
            break;
          }
          k += 1;
        }
        if (!done) fail("unterminated type annotation");
        i = k;
        continue;
      }
      out += ch;
      i += 1;
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

let count = 0;
for (const dir of inputDirs) {
  const srcDir = join(root, dir);
  const outDir = join(outRoot, dir);
  mkdirSync(outDir, { recursive: true });
  for (const entry of readdirSync(srcDir)) {
    if (!entry.endsWith(".ts")) continue;
    const src = readFileSync(join(srcDir, entry), "utf8");
    const stripped = stripTypes(src, join(dir, entry));
    writeFileSync(join(outDir, entry.replace(/\.ts$/, ".js")), stripped);
    count += 1;
  }
}
writeFileSync(join(outRoot, "package.json"), JSON.stringify({ type: "module" }) + "\n");
console.log("stripped " + count + " file(s) into " + outRoot);
