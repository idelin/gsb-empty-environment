#!/usr/bin/env node
/*
 * Minimal TypeScript type eraser + build script.
 *
 * The sources in src/ and test/ are written in a restricted, fully erasable
 * TypeScript subset so this file can strip types without a real TS compiler
 * (the sandbox has no network access to install one). Supported constructs:
 *   - interface declarations            (dropped entirely)
 *   - type aliases                      (dropped entirely)
 *   - `import type ...`                 (dropped entirely)
 *   - parameter / return / variable type annotations
 *   - optional parameters (`a?: T`)
 * Deliberately NOT supported (kept out of the sources by convention):
 *   - enums, namespaces, classes, parameter properties, decorators
 *   - generic type arguments (`new Map<K,V>()`, `f<T>()`) — annotate the
 *     variable instead, e.g. `const m: Map<K, V> = new Map();`
 *   - `as` casts, non-null `!`, labeled statements
 *   - function types in return position (`(): () => void => ...`)
 * The stripper throws on unsupported constructs instead of emitting bad JS.
 */
import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { join, dirname, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

function isWordChar(c) {
  return c !== undefined && /[A-Za-z0-9_$]/.test(c);
}

function isWordStart(c) {
  return c !== undefined && /[A-Za-z_$]/.test(c);
}

export function stripTypes(src) {
  const n = src.length;
  const out = [];
  const stack = []; // { kind: 'paren' | 'bracket' | 'block' | 'object', ternary: number }
  let lastToken = '';
  let expectVarName = false;

  function skipWsAndComments(j) {
    for (;;) {
      while (j < n && /\s/.test(src[j])) j++;
      if (src.startsWith('//', j)) {
        const e = src.indexOf('\n', j);
        j = e === -1 ? n : e + 1;
        continue;
      }
      if (src.startsWith('/*', j)) {
        const e = src.indexOf('*/', j + 2);
        if (e === -1) throw new Error('unterminated block comment');
        j = e + 2;
        continue;
      }
      return j;
    }
  }

  function skipString(j, quote) {
    j++;
    while (j < n) {
      const c = src[j];
      if (c === '\\') {
        j += 2;
        continue;
      }
      if (c === quote) return j + 1;
      j++;
    }
    throw new Error('unterminated string literal');
  }

  function skipTemplate(j) {
    j++;
    let exprDepth = 0;
    while (j < n) {
      const c = src[j];
      if (c === '\\') {
        j += 2;
        continue;
      }
      if (c === '`' && exprDepth === 0) return j + 1;
      if (c === '$' && src[j + 1] === '{') {
        exprDepth++;
        j += 2;
        continue;
      }
      if (c === '}' && exprDepth > 0) {
        exprDepth--;
        j++;
        continue;
      }
      j++;
    }
    throw new Error('unterminated template literal');
  }
  // Consume a type expression starting at j; returns the end index.
  //   mode 'param':  stop at top-level , ) = ;   (`=>` continues)
  //   mode 'var':    stop at top-level , = ;     (`=>` continues)
  //   mode 'return': stop at top-level { => ; ,
  function consumeType(j, mode) {
    j = skipWsAndComments(j);
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"') {
        j = skipString(j, c);
        continue;
      }
      if (c === '`') {
        j = skipTemplate(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsAndComments(j);
        continue;
      }
      if (c === '{' && depth === 0 && mode === 'return') return j;
      if (c === '(' || c === '[' || c === '{' || c === '<') {
        depth++;
        j++;
        continue;
      }
      if (c === ')' || c === ']' || c === '}' || c === '>') {
        if (depth === 0) {
          if (c === ')' && mode === 'param') return j;
          j++;
          continue;
        }
        depth--;
        j++;
        continue;
      }
      if (depth === 0 && (c === ',' || c === ';')) return j;
      if (depth === 0 && c === '=') {
        if (src[j + 1] === '>') {
          if (mode === 'return') return j;
          j += 2;
          continue;
        }
        return j;
      }
      j++;
    }
    return j;
  }

  function consumeBalancedBraces(j) {
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"') {
        j = skipString(j, c);
        continue;
      }
      if (c === '`') {
        j = skipTemplate(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsAndComments(j);
        continue;
      }
      if (c === '{') depth++;
      else if (c === '}') {
        depth--;
        if (depth === 0) return j + 1;
      }
      j++;
    }
    throw new Error('unterminated braces');
  }

  // Consume from j through the next top-level ';' (used for type aliases and
  // `import type` statements).
  function consumeToSemicolon(j) {
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"') {
        j = skipString(j, c);
        continue;
      }
      if (c === '`') {
        j = skipTemplate(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsAndComments(j);
        continue;
      }
      if (c === '=' && src[j + 1] === '>') {
        j += 2;
        continue;
      }
      if (c === '(' || c === '[' || c === '{' || c === '<') depth++;
      else if (c === ')' || c === ']' || c === '}' || c === '>') {
        if (depth > 0) depth--;
      } else if (c === ';' && depth === 0) return j + 1;
      j++;
    }
    return j;
  }

  function peekWord(j) {
    j = skipWsAndComments(j);
    if (!isWordStart(src[j])) return null;
    let m = j + 1;
    while (m < n && isWordChar(src[m])) m++;
    return [src.slice(j, m), m];
  }

  function isTypeAliasAt(j) {
    const pw = peekWord(j);
    if (!pw) return false;
    const k = skipWsAndComments(pw[1]);
    return src[k] === '=' || src[k] === '<';
  }

  function consumeInterface(j) {
    j = skipWsAndComments(j);
    while (j < n && isWordChar(src[j])) j++; // interface name
    j = skipWsAndComments(j);
    while (j < n && src[j] !== '{') j++; // heritage clause (unused in our sources)
    return consumeBalancedBraces(j);
  }

  const OBJECT_PREV = new Set(['(', '[', '{', ',', '=', ':', '?', 'return', 'const', 'let', 'var']);
  let i = 0;
  while (i < n) {
    const c = src[i];

    if (c === "'" || c === '"') {
      const e = skipString(i, c);
      out.push(src.slice(i, e));
      lastToken = 'literal';
      i = e;
      continue;
    }
    if (c === '`') {
      const e = skipTemplate(i);
      out.push(src.slice(i, e));
      lastToken = 'literal';
      i = e;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      let e = src.indexOf('\n', i);
      if (e === -1) e = n;
      out.push(src.slice(i, e));
      i = e;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const e = src.indexOf('*/', i + 2);
      if (e === -1) throw new Error('unterminated block comment');
      out.push(src.slice(i, e + 2));
      i = e + 2;
      continue;
    }

    if (isWordStart(c)) {
      let j = i + 1;
      while (j < n && isWordChar(src[j])) j++;
      const word = src.slice(i, j);

      if (word === 'export') {
        const pw = peekWord(j);
        if (pw && (pw[0] === 'interface' || (pw[0] === 'type' && isTypeAliasAt(pw[1])))) {
          i = j; // drop `export`; the interface/alias handler drops the rest
          continue;
        }
        out.push(word);
        lastToken = word;
        i = j;
        continue;
      }
      if (word === 'interface') {
        i = consumeInterface(j);
        continue;
      }
      if (word === 'type' && isTypeAliasAt(j)) {
        i = consumeToSemicolon(j);
        continue;
      }
      if (word === 'import') {
        const pw = peekWord(j);
        if (pw && pw[0] === 'type') {
          i = consumeToSemicolon(pw[1]);
          continue;
        }
        out.push(word);
        lastToken = word;
        i = j;
        continue;
      }
      if (word === 'as') {
        throw new Error('`as` casts are not supported (offset ' + i + ')');
      }
      if (
        word === 'enum' ||
        word === 'namespace' ||
        word === 'class' ||
        word === 'implements' ||
        word === 'declare'
      ) {
        throw new Error('unsupported TS construct `' + word + '` (offset ' + i + ')');
      }

      out.push(word);
      lastToken = word;
      i = j;

      if (word === 'const' || word === 'let' || word === 'var') {
        expectVarName = true;
      } else if (expectVarName) {
        expectVarName = false;
        const k = skipWsAndComments(i);
        if (src[k] === ':') {
          i = consumeType(k + 1, 'var');
        }
      } else {
        const top = stack[stack.length - 1];
        if (top && top.kind === 'paren' && top.ternary === 0) {
          const k = skipWsAndComments(i);
          if (src[k] === ':') {
            i = consumeType(k + 1, 'param');
          } else if (src[k] === '?' && src[k + 1] === ':') {
            i = consumeType(k + 2, 'param'); // optional parameter: drop `?` too
          }
        }
      }
      continue;
    }
    if (c === '=' && src[i + 1] === '>') {
      out.push('=>');
      lastToken = '=>';
      expectVarName = false;
      i += 2;
      continue;
    }
    if (c === '?' && src[i + 1] === '.') {
      out.push('?.');
      lastToken = '?.';
      expectVarName = false;
      i += 2;
      continue;
    }
    if (c === '?' && src[i + 1] === '?') {
      out.push('??');
      lastToken = '??';
      expectVarName = false;
      i += 2;
      continue;
    }
    if (c === '.' && src[i + 1] === '.' && src[i + 2] === '.') {
      out.push('...');
      lastToken = '...';
      expectVarName = false;
      i += 3;
      continue;
    }
    if (c === '(') {
      stack.push({ kind: 'paren', ternary: 0 });
      out.push(c);
      lastToken = '(';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === '[') {
      stack.push({ kind: 'bracket', ternary: 0 });
      out.push(c);
      lastToken = '[';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === ')') {
      stack.pop();
      out.push(c);
      lastToken = ')';
      expectVarName = false;
      i++;
      const k = skipWsAndComments(i);
      if (src[k] === ':') {
        i = consumeType(k + 1, 'return');
      }
      continue;
    }
    if (c === ']') {
      stack.pop();
      out.push(c);
      lastToken = ']';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === '{') {
      const kind = OBJECT_PREV.has(lastToken) ? 'object' : 'block';
      stack.push({ kind, ternary: 0 });
      out.push(c);
      lastToken = '{';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === '}') {
      stack.pop();
      out.push(c);
      lastToken = '}';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === '?') {
      const top = stack[stack.length - 1];
      if (top) top.ternary++;
      out.push(c);
      lastToken = '?';
      expectVarName = false;
      i++;
      continue;
    }
    if (c === ':') {
      const top = stack[stack.length - 1];
      if (top && top.ternary > 0) top.ternary--;
      out.push(c);
      lastToken = ':';
      expectVarName = false;
      i++;
      continue;
    }
    if (!/\s/.test(c)) {
      lastToken = c;
      expectVarName = false;
    }
    out.push(c);
    i++;
  }
  return out.join('');
}

function collectTsFiles(dir) {
  const files = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...collectTsFiles(p));
    else if (entry.name.endsWith('.ts')) files.push(p);
  }
  return files;
}

function main() {
  const files = [...collectTsFiles(join(root, 'src')), ...collectTsFiles(join(root, 'test'))];
  rmSync(join(root, 'build'), { recursive: true, force: true });
  for (const file of files) {
    const js = stripTypes(readFileSync(file, 'utf8'));
    const rel = relative(root, file).replace(/\.ts$/, '.js');
    const dest = join(root, 'build', rel);
    mkdirSync(dirname(dest), { recursive: true });
    writeFileSync(dest, js);
    console.log('stripped ' + rel);
  }
  console.log('build complete: ' + files.length + ' files -> build/');
}

main();
