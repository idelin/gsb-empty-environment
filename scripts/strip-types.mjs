// Minimal type erasure for src/history.ts so tests can run without a
// TypeScript compiler (offline environment). The source is written in a
// restricted, erasable-only subset of TypeScript:
//   - type annotations only in class fields, function/method params and
//     return positions (never on locals inside bodies);
//   - interfaces (stripped wholesale), single-param generics on
//     class/function declarations;
//   - no `as` casts, no enums, no parameter properties, no non-null `!`,
//     no ternaries or object-literal colons inside call parens, no
//     destructuring-with-rename in params.
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const isIdStart = (c) => /[A-Za-z_$]/.test(c);
const isIdChar = (c) => /[A-Za-z0-9_$]/.test(c);

function skipString(src, i) {
  const quote = src[i];
  let j = i + 1;
  while (j < src.length) {
    if (src[j] === "\\") j += 2;
    else if (src[j] === quote) return j + 1;
    else j++;
  }
  return j;
}

function skipTemplate(src, i) {
  let j = i + 1;
  while (j < src.length) {
    const c = src[j];
    if (c === "\\") j += 2;
    else if (c === "`") return j + 1;
    else if (c === "$" && src[j + 1] === "{") {
      let depth = 1;
      j += 2;
      while (j < src.length && depth > 0) {
        const d = src[j];
        if (d === "'" || d === '"') j = skipString(src, j);
        else if (d === "`") j = skipTemplate(src, j);
        else if (d === "{") { depth++; j++; }
        else if (d === "}") { depth--; j++; }
        else j++;
      }
    } else j++;
  }
  return j;
}

function skipComment(src, i) {
  if (src[i + 1] === "/") {
    const nl = src.indexOf("\n", i);
    return nl === -1 ? src.length : nl;
  }
  const end = src.indexOf("*/", i + 2);
  return end === -1 ? src.length : end + 2;
}

// Scans a type starting at `start`; returns the index of the terminator
// (not consumed): one of , ) ; = { } or newline at nesting depth 0.
function skipTypeAnnotation(src, start) {
  let j = start;
  let paren = 0, brace = 0, angle = 0, bracket = 0;
  const top = () => paren === 0 && brace === 0 && angle === 0 && bracket === 0;
  while (j < src.length) {
    const c = src[j];
    if (c === "'" || c === '"') { j = skipString(src, j); continue; }
    if (c === "`") { j = skipTemplate(src, j); continue; }
    if (c === "/" && (src[j + 1] === "/" || src[j + 1] === "*")) { j = skipComment(src, j); continue; }
    if (c === "=" && src[j + 1] === ">") { j += 2; continue; }
    if (c === "(") paren++;
    else if (c === ")") { if (top()) break; paren--; }
    else if (c === "{") { if (top()) break; brace++; }
    else if (c === "}") { if (top()) break; brace--; }
    else if (c === "[") bracket++;
    else if (c === "]") { if (top()) break; bracket--; }
    else if (c === "<") angle++;
    else if (c === ">") { if (angle > 0) angle--; }
    else if ((c === "," || c === ";" || c === "=" || c === "\n") && top()) break;
    j++;
  }
  return j;
}

// Matches a generic parameter list like <T> starting at src[i] === "<".
// Returns the index just after ">" or -1 when this is not a generic.
function matchGeneric(src, i) {
  let j = i + 1;
  let depth = 1;
  while (j < src.length) {
    const c = src[j];
    if (c === "<") depth++;
    else if (c === ">") {
      depth--;
      if (depth === 0) {
        let k = j + 1;
        while (src[k] === " " || src[k] === "\t") k++;
        return src[k] === "(" || src[k] === "{" ? j + 1 : -1;
      }
    } else if (!isIdChar(c) && c !== "," && c !== " " && c !== "\t") {
      return -1;
    }
    j++;
  }
  return -1;
}

function skipInterface(src, i) {
  // i is just after the `interface` keyword; skip name, then balanced braces.
  let j = i;
  while (j < src.length && src[j] !== "{") j++;
  let depth = 0;
  while (j < src.length) {
    const c = src[j];
    if (c === "'" || c === '"') { j = skipString(src, j); continue; }
    if (c === "{") depth++;
    else if (c === "}") { depth--; if (depth === 0) return j + 1; }
    j++;
  }
  return j;
}

export function stripTypes(src) {
  let out = "";
  let i = 0;
  let paren = 0, brace = 0;
  while (i < src.length) {
    const c = src[i];
    if (c === "'" || c === '"') {
      const j = skipString(src, i);
      out += src.slice(i, j); i = j; continue;
    }
    if (c === "`") {
      const j = skipTemplate(src, i);
      out += src.slice(i, j); i = j; continue;
    }
    if (c === "/" && (src[i + 1] === "/" || src[i + 1] === "*")) {
      const j = skipComment(src, i);
      out += src.slice(i, j); i = j; continue;
    }
    if (c === "(") { paren++; out += c; i++; continue; }
    if (c === ")") { paren--; out += c; i++; continue; }
    if (c === "{") { brace++; out += c; i++; continue; }
    if (c === "}") { brace--; out += c; i++; continue; }
    if (isIdStart(c)) {
      let j = i + 1;
      while (j < src.length && isIdChar(src[j])) j++;
      const word = src.slice(i, j);
      if (word === "interface") {
        // Drop a dangling `export` left in the output.
        out = out.replace(/export\s*$/, "");
        i = skipInterface(src, j);
        continue;
      }
      out += word;
      i = j;
      if (src[i] === "<") {
        const end = matchGeneric(src, i);
        if (end !== -1) i = end;
      }
      continue;
    }
    if (c === ":" && (paren >= 1 || brace <= 1)) {
      // Type annotation: drop a preceding "?" (optional marker) too.
      const trimmed = out.replace(/\s+$/, "");
      if (trimmed.endsWith("?")) out = trimmed.slice(0, -1);
      i = skipTypeAnnotation(src, i + 1);
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const source = readFileSync(join(root, "src", "history.ts"), "utf8");
const output = stripTypes(source);
const dest = join(root, "test", ".build", "history.mjs");
mkdirSync(dirname(dest), { recursive: true });
writeFileSync(dest, output);
console.log("stripped types -> " + dest);
