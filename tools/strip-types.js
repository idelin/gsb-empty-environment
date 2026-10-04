'use strict';

// strip-types: a small, dependency-free TypeScript -> JavaScript transform.
//
// This environment has no TypeScript compiler and no network access, so this
// loader lets the test-suite execute the .ts sources directly on Node 16.
// It supports the disciplined subset of TS used in this repo:
//
//   supported:  type annotations, interfaces, type aliases, import/export,
//               `import type`, access modifiers (private/readonly/...)
//   not used:   enums, namespaces, parameter properties, decorators, generics
//               in expression position, ternaries, switch statements, labels
//
// In a networked environment, prefer the real compiler: `npm run typecheck`.

const MODIFIERS = new Set([
  'private', 'public', 'protected', 'readonly', 'abstract', 'declare', 'override',
]);
const UNSUPPORTED = new Set(['enum', 'namespace', 'implements', 'satisfies', 'asserts']);

function isIdStart(c) {
  return /[A-Za-z_$]/.test(c);
}
function isIdChar(c) {
  return /[A-Za-z0-9_$]/.test(c);
}

function stripTypes(src, filename) {
  const n = src.length;
  let i = 0;
  let out = '';
  const exportNames = [];
  const stack = []; // '(' | '[' | 'object' | 'block'
  let prevToken = '';
  let lastChar = ''; // last significant (non-ws, non-comment) char scanned
  let classPending = false;

  function lineOf(pos) {
    return src.slice(0, pos).split('\n').length;
  }
  function fail(msg, pos) {
    const at = pos === undefined ? i : pos;
    throw new Error(filename + ':' + lineOf(at) + ': strip-types: ' + msg);
  }
  function skipWsComments(j) {
    for (;;) {
      while (j < n && /\s/.test(src[j])) j++;
      if (src[j] === '/' && src[j + 1] === '/') {
        const e = src.indexOf('\n', j);
        j = e < 0 ? n : e;
        continue;
      }
      if (src[j] === '/' && src[j + 1] === '*') {
        const e = src.indexOf('*/', j + 2);
        if (e < 0) fail('unterminated block comment', j);
        j = e + 2;
        continue;
      }
      return j;
    }
  }
  function skipString(j) {
    const q = src[j];
    j++;
    while (j < n) {
      const c = src[j];
      if (c === '\\') {
        j += 2;
        continue;
      }
      if (c === q) return j + 1;
      j++;
    }
    fail('unterminated string', j);
  }
  function skipBalancedBraces(j) {
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') {
        j = skipString(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsComments(j);
        continue;
      }
      if (c === '{') depth++;
      else if (c === '}') {
        depth--;
        if (depth === 0) return j + 1;
      }
      j++;
    }
    fail('unbalanced braces', j);
  }
  function readWord(j) {
    const s = skipWsComments(j);
    if (s >= n || !isIdStart(src[s])) return { word: '', start: s, end: s };
    let e = s + 1;
    while (e < n && isIdChar(src[e])) e++;
    return { word: src.slice(s, e), start: s, end: e };
  }
  function skipSemicolon() {
    let j = i;
    while (j < n && (src[j] === ' ' || src[j] === '\t' || src[j] === '\r')) j++;
    if (src[j] === ';') i = j + 1;
  }

  // i at ':' -> skip the following type annotation (emits nothing).
  function skipAnnotation() {
    let j = i + 1;
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') {
        j = skipString(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsComments(j);
        continue;
      }
      if (c === '=' && src[j + 1] === '>' && depth > 0) {
        j += 2;
        continue;
      }
      if (c === '<' || c === '(' || c === '[') {
        depth++;
        j++;
        continue;
      }
      if (c === '>' || c === ')' || c === ']') {
        if (depth === 0) break;
        depth--;
        j++;
        continue;
      }
      if (c === '{') {
        if (depth === 0) break;
        depth++;
        j++;
        continue;
      }
      if (c === '}') {
        if (depth === 0) break;
        depth--;
        j++;
        continue;
      }
      if (depth === 0 && (c === ',' || c === '=' || c === ';' || c === '\n')) break;
      j++;
    }
    i = j;
    prevToken = ')';
    lastChar = ')';
  }

  function skipInterface() {
    let j = i + 'interface'.length;
    while (j < n && src[j] !== '{') {
      if (src[j] === "'" || src[j] === '"' || src[j] === '`') {
        j = skipString(j);
        continue;
      }
      j++;
    }
    i = skipBalancedBraces(j);
    prevToken = ';';
    lastChar = ';';
  }

  function looksLikeTypeAlias(j) {
    const w = readWord(j + 4);
    if (!w.word) return false;
    const k = skipWsComments(w.end);
    return src[k] === '=' || src[k] === '<';
  }

  function skipTypeAlias() {
    let j = i + 4;
    let depth = 0;
    while (j < n) {
      const c = src[j];
      if (c === "'" || c === '"' || c === '`') {
        j = skipString(j);
        continue;
      }
      if (c === '/' && (src[j + 1] === '/' || src[j + 1] === '*')) {
        j = skipWsComments(j);
        continue;
      }
      if (c === '=' && src[j + 1] === '>') {
        j += 2;
        continue;
      }
      if (c === '<' || c === '(' || c === '[' || c === '{') depth++;
      else if (c === '>' || c === ')' || c === ']' || c === '}') {
        if (depth > 0) depth--;
      } else if (c === ';' && depth === 0) {
        j++;
        break;
      }
      j++;
    }
    i = j;
    prevToken = ';';
    lastChar = ';';
  }

  function handleImport() {
    const j = skipWsComments(i + 6);
    const c = src[j];
    if (c === "'" || c === '"') {
      const e = skipString(j);
      out += 'require(' + src.slice(j, e) + ');';
      i = e;
      skipSemicolon();
      prevToken = ';';
      lastChar = ';';
      return;
    }
    if (c === '{') {
      const e = skipBalancedBraces(j);
      const inner = src.slice(j + 1, e - 1);
      const w = readWord(e);
      if (w.word !== 'from') fail('expected `from` in import', e);
      const p = skipWsComments(w.end);
      const pe = skipString(p);
      const path = src.slice(p, pe);
      const parts = [];
      for (const raw of inner.split(',')) {
        const spec = raw.trim();
        if (!spec) continue;
        if (spec.indexOf('type ') === 0) continue;
        const m = /^(\S+)\s+as\s+(\S+)$/.exec(spec);
        if (m) parts.push(m[1] + ': ' + m[2]);
        else parts.push(spec);
      }
      if (parts.length > 0) {
        out += 'const { ' + parts.join(', ') + ' } = require(' + path + ');';
      }
      i = pe;
      skipSemicolon();
      prevToken = ';';
      lastChar = ';';
      return;
    }
    if (c === '*') {
      const asW = readWord(j + 1);
      if (asW.word !== 'as') fail('expected `as` after `*`', j);
      const ns = readWord(asW.end);
      const from = readWord(ns.end);
      if (from.word !== 'from') fail('expected `from` in import', from.start);
      const p = skipWsComments(from.end);
      const pe = skipString(p);
      out += 'const ' + ns.word + ' = require(' + src.slice(p, pe) + ');';
      i = pe;
      skipSemicolon();
      prevToken = ';';
      lastChar = ';';
      return;
    }
    const w = readWord(j);
    if (w.word === 'type') {
      const after = readWord(w.end);
      if (after.word !== 'from') {
        // `import type ...` -> drop entirely
        let k = w.end;
        while (k < n && src[k] !== ';') k++;
        i = k + 1;
        prevToken = ';';
        lastChar = ';';
        return;
      }
      fail('default import named `type` is not supported', j);
    }
    if (w.word) {
      const from = readWord(w.end);
      if (from.word === 'from') {
        const p = skipWsComments(from.end);
        const pe = skipString(p);
        out += 'const ' + w.word + ' = require(' + src.slice(p, pe) + ');';
        i = pe;
        skipSemicolon();
        prevToken = ';';
        lastChar = ';';
        return;
      }
    }
    fail('unsupported import form', j);
  }

  function handleExport() {
    const j = skipWsComments(i + 6);
    const c = src[j];
    if (c === '{') {
      const e = skipBalancedBraces(j);
      const inner = src.slice(j + 1, e - 1);
      const lines = [];
      for (const raw of inner.split(',')) {
        const spec = raw.trim();
        if (!spec) continue;
        const m = /^(\S+)\s+as\s+(\S+)$/.exec(spec);
        if (m) lines.push('exports.' + m[2] + ' = ' + m[1] + ';');
        else lines.push('exports.' + spec + ' = ' + spec + ';');
      }
      out += lines.join('\n');
      i = e;
      skipSemicolon();
      prevToken = ';';
      lastChar = ';';
      return;
    }
    const w = readWord(j);
    if (w.word === 'default') fail('export default is not supported', j);
    if (w.word === 'interface') {
      i = w.start;
      skipInterface();
      return;
    }
    if (w.word === 'type' && looksLikeTypeAlias(w.start)) {
      i = w.start;
      skipTypeAlias();
      return;
    }
    if (w.word === 'async') {
      const w2 = readWord(w.end);
      if (w2.word !== 'function') fail('unsupported `export async` form', j);
      const name = readWord(w2.end);
      exportNames.push(name.word);
      i = j;
      return;
    }
    if (
      w.word === 'function' ||
      w.word === 'class' ||
      w.word === 'const' ||
      w.word === 'let' ||
      w.word === 'var'
    ) {
      const name = readWord(w.end);
      if (name.word) exportNames.push(name.word);
      i = j;
      return;
    }
    fail('unsupported export form', j);
  }

  while (i < n) {
    const c = src[i];

    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      out += c;
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      let e = src.indexOf('\n', i);
      if (e < 0) e = n;
      out += src.slice(i, e);
      i = e;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const e = src.indexOf('*/', i + 2);
      if (e < 0) fail('unterminated block comment');
      out += src.slice(i, e + 2);
      i = e + 2;
      continue;
    }
    if (c === "'" || c === '"' || c === '`') {
      const e = skipString(i);
      out += src.slice(i, e);
      i = e;
      prevToken = 'lit';
      lastChar = 'x';
      continue;
    }
    if (isIdStart(c)) {
      let e = i + 1;
      while (e < n && isIdChar(src[e])) e++;
      const w = src.slice(i, e);
      if (lastChar !== '.') {
        if (MODIFIERS.has(w)) {
          i = e;
          continue;
        }
        if (UNSUPPORTED.has(w)) fail('unsupported TypeScript construct: ' + w);
        if (w === 'import') {
          handleImport();
          continue;
        }
        if (w === 'export') {
          handleExport();
          continue;
        }
        if (w === 'interface') {
          skipInterface();
          continue;
        }
        if (w === 'type' && looksLikeTypeAlias(i)) {
          skipTypeAlias();
          continue;
        }
        if (w === 'class') classPending = true;
      }
      out += w;
      i = e;
      prevToken = w;
      lastChar = w[w.length - 1];
      continue;
    }
    if (c === ':') {
      const top = stack[stack.length - 1];
      if (top === 'object') {
        out += c;
        i++;
        prevToken = ':';
        lastChar = ':';
        continue;
      }
      skipAnnotation();
      continue;
    }
    if (c === '{') {
      let kind = 'object';
      if (
        classPending ||
        prevToken === ')' ||
        prevToken === '}' ||
        prevToken === ';' ||
        prevToken === '{' ||
        prevToken === '' ||
        prevToken === 'else' ||
        prevToken === 'do' ||
        prevToken === 'try' ||
        prevToken === 'finally' ||
        prevToken === '=>'
      ) {
        kind = 'block';
      }
      classPending = false;
      stack.push(kind);
      out += c;
      i++;
      prevToken = '{';
      lastChar = '{';
      continue;
    }
    if (c === '}') {
      stack.pop();
      out += c;
      i++;
      prevToken = '}';
      lastChar = '}';
      continue;
    }
    if (c === '(' || c === '[') {
      stack.push(c);
      out += c;
      i++;
      prevToken = c;
      lastChar = c;
      continue;
    }
    if (c === ')' || c === ']') {
      stack.pop();
      out += c;
      i++;
      prevToken = c;
      lastChar = c;
      continue;
    }
    if (c === '=' && src[i + 1] === '>') {
      out += '=>';
      i += 2;
      prevToken = '=>';
      lastChar = '>';
      continue;
    }
    if (/[0-9]/.test(c)) {
      let e = i + 1;
      while (e < n && /[0-9A-Fa-fxXoObB_.]/.test(src[e])) e++;
      out += src.slice(i, e);
      i = e;
      prevToken = 'lit';
      lastChar = '0';
      continue;
    }
    out += c;
    i++;
    prevToken = c;
    lastChar = c;
  }

  if (exportNames.length > 0) {
    out += '\n';
    for (const name of exportNames) {
      out += 'exports.' + name + ' = ' + name + ';\n';
    }
  }
  return out;
}

module.exports = stripTypes;
