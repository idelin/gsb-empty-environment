#!/usr/bin/env node
// Zero-dependency TypeScript -> JavaScript build for offline environments.
//
// The sources in src/ are written in the "erasable syntax only" subset of
// TypeScript, so this script strips type annotations and emits runnable ESM
// JavaScript into dist/. Every emitted file is validated with `node --check`
// and the whole test suite runs against dist/, so a broken strip fails loudly.
//
// Source constraints (enforced by convention, see README):
//   - no enum / namespace / class / decorators / parameter properties
//   - no non-null assertions (`x!`), no `satisfies`
//   - `as` casts only in expressions (never in import/export), simple types
//   - no call-site generics (`f<T>(...)`); annotate the variable instead
//   - no regex literals (use `new RegExp` if ever needed)
//   - type annotations stay on a single line

import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const srcDir = join(root, 'src');
const outDir = join(root, 'dist');

const ID_START = /[A-Za-z_$]/;
const ID_CHAR = /[A-Za-z0-9_$]/;
const WS = /\s/;

export function stripTypes(src) {
  const n = src.length;
  let out = '';
  let i = 0;

  const isIdStart = (p) => p < n && ID_START.test(src[p]);
  const isIdChar = (p) => p < n && ID_CHAR.test(src[p]);

  function readWord(pos) {
    let j = pos;
    while (j < n && isIdChar(j)) j++;
    return src.slice(pos, j);
  }

  function skipWs(pos) {
    let j = pos;
    for (;;) {
      while (j < n && WS.test(src[j])) j++;
      if (src[j] === '/' && src[j + 1] === '/') {
        while (j < n && src[j] !== '\n') j++;
        continue;
      }
      if (src[j] === '/' && src[j + 1] === '*') {
        const e = src.indexOf('*/', j + 2);
        j = e === -1 ? n : e + 2;
        continue;
      }
      return j;
    }
  }

  function skipStringLike(pos) {
    const quote = src[pos];
    if (quote === '`') {
      let j = pos + 1;
      let depth = 0;
      while (j < n) {
        const c = src[j];
        if (c === '\\') { j += 2; continue; }
        if (c === '$' && src[j + 1] === '{') { depth++; j += 2; continue; }
        if (c === '}' && depth > 0) { depth--; j++; continue; }
        if (c === '`' && depth === 0) return j + 1;
        j++;
      }
      return n;
    }
    let j = pos + 1;
    while (j < n && src[j] !== quote) {
      if (src[j] === '\\') j++;
      j++;
    }
    return j + 1;
  }

  function skipComment(pos) {
    if (src[pos + 1] === '/') {
      const e = src.indexOf('\n', pos);
      return e === -1 ? n : e;
    }
    const e = src.indexOf('*/', pos + 2);
    return e === -1 ? n : e + 2;
  }

  // Copies a string/comment starting at i into out. Returns true if handled.
  function copyVerbatim() {
    const c = src[i];
    if (c === "'" || c === '"' || c === '`') {
      const e = skipStringLike(i);
      out += src.slice(i, e);
      i = e;
      return true;
    }
    if (c === '/' && (src[i + 1] === '/' || src[i + 1] === '*')) {
      const e = skipComment(i);
      out += src.slice(i, e);
      i = e;
      return true;
    }
    return false;
  }

  // Index of the closer matching the bracket at pos. Skips strings/comments.
  function findMatching(pos) {
    const open = src[pos];
    const close = { '(': ')', '[': ']', '{': '}', '<': '>' }[open];
    let depth = 0;
    let j = pos;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') { j = skipStringLike(j); continue; }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) { j = skipComment(j); continue; }
      if (c === open) depth++;
      else if (c === close) {
        depth--;
        if (depth === 0) return j;
      }
      j++;
    }
    throw new Error(`strip-types: unbalanced '${open}' at offset ${pos}`);
  }

  // Skip a type expression starting at pos. stop(ch, pos) at bracket depth 0
  // ends the type (the stopper character is not consumed).
  function skipTypeExpr(pos, stop) {
    let j = pos;
    const stack = [];
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') { j = skipStringLike(j); continue; }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) { j = skipComment(j); continue; }
      if (stack.length === 0 && stop(c, j)) break;
      if (c === '(' || c === '[' || c === '{' || c === '<') stack.push(c);
      else if (c === ')' || c === ']' || c === '}' || c === '>') {
        if (stack.length === 0) {
          if (c === '>') { j++; continue; }
          break;
        }
        stack.pop();
      }
      j++;
    }
    return j;
  }

  // Strip annotations inside a parameter list [openPos, closePos].
  function stripParams(openPos, closePos) {
    let inner = '';
    let j = openPos + 1;
    let depth = 0;
    while (j < closePos) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') {
        const e = skipStringLike(j);
        inner += src.slice(j, e);
        j = e;
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        const e = skipComment(j);
        inner += src.slice(j, e);
        j = e;
        continue;
      }
      if (depth === 0 && c === '?') {
        const k = skipWs(j + 1);
        if (src[k] === ':') { j = k; continue; }
        inner += c;
        j++;
        continue;
      }
      if (depth === 0 && c === ':') {
        j = skipTypeExpr(j + 1, (ch, p) =>
          ch === ',' || (ch === '=' && src[p + 1] !== '>'));
        continue;
      }
      if (depth === 0 && c === '=') {
        inner += c;
        j++;
        let d2 = 0;
        while (j < closePos) {
          const ch = src[j];
          if (ch === "'" || ch === '"' || ch === '`') {
            const e = skipStringLike(j);
            inner += src.slice(j, e);
            j = e;
            continue;
          }
          if (ch === ',' && d2 === 0) break;
          if (ch === '(' || ch === '[' || ch === '{') d2++;
          else if (ch === ')' || ch === ']' || ch === '}') {
            if (d2 === 0) break;
            d2--;
          }
          inner += ch;
          j++;
        }
        continue;
      }
      if (c === '(' || c === '[' || c === '{') depth++;
      else if (c === ')' || c === ']' || c === '}') depth--;
      inner += c;
      j++;
    }
    return '(' + inner + ')';
  }

  const returnTypeStop = (ch, p) =>
    ch === '{' || (ch === '=' && src[p + 1] === '>');

  while (i < n) {
    if (copyVerbatim()) continue;
    const c = src[i];

    if (isIdStart(i)) {
      const word = readWord(i);
      const after = i + word.length;

      if (word === 'interface') {
        let k = skipWs(after);
        k = skipWs(k + readWord(k).length);
        while (src[k] !== '{') {
          if (src[k] === '<') k = findMatching(k) + 1;
          else k++;
        }
        i = findMatching(k) + 1;
        continue;
      }

      if (word === 'type') {
        const k = skipWs(after);
        if (isIdStart(k)) {
          const m = skipWs(k + readWord(k).length);
          if (src[m] === '=' || src[m] === '<') {
            i = skipTypeExpr(m, (ch) => ch === ';') + 1;
            continue;
          }
        }
        out += word;
        i = after;
        continue;
      }

      if (word === 'import' || word === 'export') {
        const k = skipWs(after);
        if (isIdStart(k)) {
          const w2 = readWord(k);
          if (w2 === 'type') {
            i = skipTypeExpr(skipWs(k + 4), (ch) => ch === ';') + 1;
            continue;
          }
          if (w2 === 'interface') {
            i = k;
            continue;
          }
        }
        out += word;
        i = after;
        continue;
      }

      if (word === 'function') {
        out += word;
        let k = after;
        for (;;) {
          while (k < n && WS.test(src[k])) { out += src[k]; k++; }
          const ch = src[k];
          if (isIdStart(k) || ch === '*') {
            out += src.slice(k, k + (ch === '*' ? 1 : readWord(k).length));
            k += ch === '*' ? 1 : readWord(k).length;
            continue;
          }
          if (ch === '<') { k = findMatching(k) + 1; continue; }
          break;
        }
        if (src[k] === '(') {
          const closePos = findMatching(k);
          out += stripParams(k, closePos);
          k = skipWs(closePos + 1);
          if (src[k] === ':') k = skipTypeExpr(k + 1, returnTypeStop);
          i = k;
          continue;
        }
        i = k;
        continue;
      }

      if (word === 'as') {
        const k = skipWs(after);
        i = skipTypeExpr(k, (ch, p) =>
          ch === ')' || ch === ',' || ch === ';' || ch === ']' || ch === '}' ||
          ch === '\n' || ch === '?' || ch === ':' ||
          (ch === '&' && src[p + 1] === '&') ||
          (ch === '|' && src[p + 1] === '|') ||
          (ch === '=' && src[p + 1] !== '>') ||
          ch === '+' || ch === '*' || ch === '/' || ch === '%');
        continue;
      }

      if (word === 'const' || word === 'let' || word === 'var') {
        out += word;
        i = after;
        const k = skipWs(i);
        if (isIdStart(k)) {
          const e = k + readWord(k).length;
          const m = skipWs(e);
          if (src[m] === ':') {
            out += src.slice(i, e);
            i = skipTypeExpr(m + 1, (ch, p) =>
              (ch === '=' && src[p + 1] !== '>') || ch === ';' || ch === ',' || ch === '\n');
          }
        }
        continue;
      }

      out += word;
      i = after;
      continue;
    }

    if (c === '(') {
      const closePos = findMatching(i);
      const k = skipWs(closePos + 1);
      if (src[k] === '=' && src[k + 1] === '>') {
        out += stripParams(i, closePos);
        i = closePos + 1;
        continue;
      }
      if (src[k] === ':') {
        const e = skipTypeExpr(k + 1, (ch, p) =>
          (ch === '=' && src[p + 1] === '>') ||
          ch === ';' || ch === ',' || ch === ')' || ch === ']' || ch === '}');
        const m = skipWs(e);
        if (src[m] === '=' && src[m + 1] === '>') {
          out += stripParams(i, closePos);
          i = m;
          continue;
        }
      }
      out += c;
      i++;
      continue;
    }

    out += c;
    i++;
  }

  return out;
}

function main() {
  const files = readdirSync(srcDir).filter((f) => f.endsWith('.ts')).sort();
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true });
  for (const file of files) {
    const src = readFileSync(join(srcDir, file), 'utf8');
    const js = stripTypes(src);
    const outFile = join(outDir, file.replace(/\.ts$/, '.js'));
    writeFileSync(outFile, js);
    execFileSync(process.execPath, ['--check', outFile], { stdio: 'pipe' });
    console.log(`built src/${file} -> dist/${file.replace(/\.ts$/, '.js')}`);
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main();
}
