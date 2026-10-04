/**
 * Minimal TypeScript type eraser -> dist/*.js
 *
 * The sandbox has no TypeScript compiler and no network access, so this
 * script strips type annotations from src/*.ts to produce runnable ESM.
 * It supports exactly the disciplined subset used in src/:
 *   - interface / type-alias declarations
 *   - parameter, return-type, variable and class-field annotations
 *   - access modifiers (private/public/protected/readonly/abstract)
 *   - implements clauses, import type / export type
 * It deliberately does NOT support: generics syntax, enums, `as` casts,
 * non-null assertions, optional parameters, parameter properties,
 * namespaced or decorator syntax. src/ avoids all of these.
 */
import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, "..", "src");
const OUT = join(here, "..", "dist");

const isIdStart = (c) => /[A-Za-z_$]/.test(c);
const isIdChar = (c) => /[A-Za-z0-9_$]/.test(c);

export function stripTypes(src) {
  const n = src.length;
  let out = "";
  let i = 0;
  const scopes = []; // "class" | "block"
  let pendingClass = false;
  let declaratorMode = false;
  let nextParenIsParams = false;
  let paramDepth = 0;
  let parenDepth = 0;

  function peek(j) {
    while (j < n && /\s/.test(src[j])) j++;
    return j;
  }

  function lastOut() {
    for (let j = out.length - 1; j >= 0; j--) {
      if (!/\s/.test(out[j])) return out[j];
    }
    return "";
  }

  function skipString(j) {
    const quote = src[j];
    j++;
    while (j < n) {
      const c = src[j];
      if (c === "\\") j += 2;
      else if (c === quote) return j + 1;
      else j++;
    }
    return j;
  }

  function skipTypeAnnotation(j) {
    // src[j] === ':'; consumes the annotation, stops before its terminator.
    j++;
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (depth === 0 && (c === "," || c === ")" || c === "=" || c === ";" || c === "{" || c === "}")) break;
      if (c === "(" || c === "[" || c === "{" || c === "<") depth++;
      else if (c === ")" || c === "]" || c === "}" || c === ">") {
        if (depth === 0) break;
        depth--;
      }
      j++;
    }
    return j;
  }

  function skipBalancedBraces(j) {
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "{") depth++;
      else if (c === "}") {
        depth--;
        if (depth === 0) return j + 1;
      } else if (c === '"' || c === "'" || c === "`") {
        j = skipString(j) - 1;
      }
      j++;
    }
    return j;
  }

  function findMatchingParen(j) {
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "(") depth++;
      else if (c === ")") {
        depth--;
        if (depth === 0) return j;
      } else if (c === '"' || c === "'" || c === "`") {
        j = skipString(j) - 1;
      } else if (c === "/" && src[j + 1] === "/") {
        const k = src.indexOf("\n", j);
        j = (k === -1 ? n : k) - 1;
      } else if (c === "/" && src[j + 1] === "*") {
        const k = src.indexOf("*/", j + 2);
        j = (k === -1 ? n : k + 2) - 1;
      }
      j++;
    }
    return -1;
  }

  while (i < n) {
    const c = src[i];

    if (c === "/" && src[i + 1] === "/") {
      let j = src.indexOf("\n", i);
      if (j === -1) j = n;
      out += src.slice(i, j);
      i = j;
      continue;
    }
    if (c === "/" && src[i + 1] === "*") {
      let j = src.indexOf("*/", i + 2);
      j = j === -1 ? n : j + 2;
      out += src.slice(i, j);
      i = j;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      const j = skipString(i);
      out += src.slice(i, j);
      i = j;
      continue;
    }

    if (isIdStart(c)) {
      let j = i;
      while (j < n && isIdChar(src[j])) j++;
      const word = src.slice(i, j);
      const k = peek(j);
      const prev = lastOut();
      const prevIsMember = prev === "." || isIdChar(prev);

      if (word === "export" && !prevIsMember) {
        let m = k;
        while (m < n && isIdChar(src[m])) m++;
        const nextWord = src.slice(k, m);
        if (nextWord === "interface" || nextWord === "type") {
          i = k; // drop `export`, the declaration handler removes the rest
          continue;
        }
        out += word;
        i = j;
        continue;
      }

      if (word === "import" && !prevIsMember) {
        let m = k;
        while (m < n && isIdChar(src[m])) m++;
        if (src.slice(k, m) === "type") {
          const e = src.indexOf(";", m);
          i = e === -1 ? n : e + 1;
          continue;
        }
        out += word;
        i = j;
        continue;
      }

      if (word === "interface" && !prevIsMember) {
        const b = src.indexOf("{", j);
        i = b === -1 ? n : skipBalancedBraces(b);
        continue;
      }

      if (word === "type" && !prevIsMember && k < n && isIdStart(src[k])) {
        let m = k;
        while (m < n && isIdChar(src[m])) m++;
        if (src[peek(m)] === "=") {
          const e = src.indexOf(";", m);
          i = e === -1 ? n : e + 1;
          continue;
        }
      }

      if (word === "implements" && !prevIsMember) {
        const b = src.indexOf("{", j);
        i = b === -1 ? n : b;
        continue;
      }

      if (
        (word === "private" || word === "public" || word === "protected" ||
          word === "readonly" || word === "abstract") &&
        !prevIsMember && k < n && (isIdStart(src[k]) || src[k] === "#")
      ) {
        i = j; // drop modifier
        continue;
      }

      if (word === "class") pendingClass = true;
      if (word === "function") nextParenIsParams = true;

      if (word === "const" || word === "let" || word === "var") {
        declaratorMode = k < n && isIdStart(src[k]);
        out += word;
        i = j;
        continue;
      }

      if (declaratorMode) {
        declaratorMode = false;
        out += word;
        i = src[k] === ":" ? skipTypeAnnotation(k) : j;
        continue;
      }

      if (scopes.length > 0 && scopes[scopes.length - 1] === "class") {
        if (src[k] === ":") {
          out += word;
          i = skipTypeAnnotation(k);
          continue;
        }
        if (src[k] === "(") nextParenIsParams = true;
        out += word;
        i = j;
        continue;
      }

      out += word;
      i = j;
      continue;
    }

    if (c === "(") {
      let isParams = nextParenIsParams;
      nextParenIsParams = false;
      if (!isParams) {
        const close = findMatchingParen(i);
        if (close !== -1) {
          const after = peek(close + 1);
          if (src[after] === "=" && src[after + 1] === ">") isParams = true;
        }
      }
      parenDepth++;
      if (isParams) paramDepth = parenDepth;
      out += c;
      i++;
      continue;
    }

    if (c === ")") {
      const wasParamClose = paramDepth !== 0 && parenDepth === paramDepth;
      parenDepth--;
      out += c;
      i++;
      if (wasParamClose) {
        paramDepth = 0;
        const k = peek(i);
        if (src[k] === ":") i = skipTypeAnnotation(k);
      }
      continue;
    }

    if (c === "{") {
      scopes.push(pendingClass ? "class" : "block");
      pendingClass = false;
      out += c;
      i++;
      continue;
    }

    if (c === "}") {
      scopes.pop();
      out += c;
      i++;
      continue;
    }

    if (c === ":" && paramDepth !== 0 && parenDepth === paramDepth) {
      i = skipTypeAnnotation(i);
      continue;
    }

    out += c;
    i++;
  }
  return out;
}

rmSync(OUT, { recursive: true, force: true });
mkdirSync(OUT, { recursive: true });
const files = readdirSync(SRC).filter((f) => f.endsWith(".ts"));
for (const f of files) {
  const js = stripTypes(readFileSync(join(SRC, f), "utf8"));
  writeFileSync(join(OUT, f.replace(/\.ts$/, ".js")), js);
}
// Loading each emitted module verifies it parses and links.
for (const f of files) {
  await import(pathToFileURL(join(OUT, f.replace(/\.ts$/, ".js"))).href);
}
console.log("built " + files.length + " files -> dist/");
