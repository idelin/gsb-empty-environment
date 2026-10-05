// Type erasure build: src/*.ts -> dist/*.js
//
// The environment has no tsc, so this script strips types from the
// TypeScript sources with a real tokenizer (not regex). The sources are
// written in an "erasable syntax only" subset of TypeScript:
//   - no enums, namespaces, decorators, parameter properties, accessors
//   - no `as` casts, no non-null `!`, no optional `?` markers in code
//     (optional fields inside interfaces are fine: interfaces are dropped)
//   - no type annotations on arrow functions (contextual typing instead)
//   - no generics on functions/classes (generic type *references* like
//     Map<string, Segment> in annotations are fine)
//   - no template literals or regex literals in src (scanner simplicity)
// If you have a real TypeScript toolchain, `npm run typecheck` (tsc
// --noEmit) type-checks the same sources.

import { readFileSync, writeFileSync, mkdirSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const SRC = new URL('../src/', import.meta.url).pathname;
const OUT = new URL('../dist/', import.meta.url).pathname;

const KEYWORDS = new Set([
  'if', 'while', 'for', 'switch', 'catch', 'with', 'return', 'typeof',
  'instanceof', 'delete', 'void', 'new', 'in', 'of', 'else', 'do', 'throw',
  'case', 'yield', 'await',
]);

const PUNCTS3 = ['===', '!==', '**=', '<<=', '>>=', '&&=', '||=', '??=', '...'];
const PUNCTS2 = [
  '=>', '==', '!=', '<=', '>=', '+=', '-=', '*=', '/=', '%=', '++', '--',
  '&&', '||', '??', '?.', '<<', '>>', '**', '&=', '|=', '^=',
];

function tokenize(src, file) {
  const tokens = [];
  let i = 0;
  let pre = '';
  const n = src.length;
  const err = (msg) => {
    throw new Error(file + ': ' + msg + ' (offset ' + i + ')');
  };
  while (i < n) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      pre += c;
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      const start = i;
      while (i < n && src[i] !== '\n') i++;
      pre += src.slice(start, i);
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const start = i;
      const end = src.indexOf('*/', i + 2);
      if (end < 0) err('unterminated block comment');
      i = end + 2;
      pre += src.slice(start, i);
      continue;
    }
    if (c === "'" || c === '"') {
      const start = i;
      i++;
      while (i < n && src[i] !== c) {
        if (src[i] === '\\') i++;
        i++;
      }
      if (i >= n) err('unterminated string');
      i++;
      tokens.push({ type: 'str', value: src.slice(start, i), pre });
      pre = '';
      continue;
    }
    if (c === '`') {
      err('template literals are not supported in src; use string concatenation');
    }
    if (/[0-9]/.test(c)) {
      const start = i;
      while (i < n && /[0-9a-zA-Z_.]/.test(src[i])) i++;
      tokens.push({ type: 'num', value: src.slice(start, i), pre });
      pre = '';
      continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      const start = i;
      while (i < n && /[A-Za-z0-9_$]/.test(src[i])) i++;
      tokens.push({ type: 'id', value: src.slice(start, i), pre });
      pre = '';
      continue;
    }
    const three = src.slice(i, i + 3);
    const two = src.slice(i, i + 2);
    if (PUNCTS3.includes(three)) {
      tokens.push({ type: 'punct', value: three, pre });
      pre = '';
      i += 3;
      continue;
    }
    if (PUNCTS2.includes(two)) {
      tokens.push({ type: 'punct', value: two, pre });
      pre = '';
      i += 2;
      continue;
    }
    tokens.push({ type: 'punct', value: c, pre });
    pre = '';
    i++;
  }
  return tokens;
}

function strip(tokens, file) {
  const n = tokens.length;
  const val = (i) => tokens[i].value;
  const isId = (i, v) => tokens[i].type === 'id' && (v === undefined || tokens[i].value === v);
  const isP = (i, v) => tokens[i].type === 'punct' && tokens[i].value === v;

  // Bracket matching.
  const match = new Array(n).fill(-1);
  const stack = [];
  for (let i = 0; i < n; i++) {
    if (tokens[i].type !== 'punct') continue;
    const v = tokens[i].value;
    if (v === '(' || v === '[' || v === '{') {
      stack.push(i);
    } else if (v === ')' || v === ']' || v === '}') {
      const j = stack.pop();
      if (j === undefined) throw new Error(file + ': unbalanced ' + v);
      match[j] = i;
      match[i] = j;
    }
  }
  if (stack.length > 0) throw new Error(file + ': unbalanced brackets');

  const dropped = new Array(n).fill(false);
  const drop = (a, b) => {
    for (let k = a; k <= b; k++) dropped[k] = true;
  };

  // Classify parameter-list parens: next token after ')' is ':', '{' or
  // '=>', and the token before '(' is a non-keyword identifier (or
  // 'function') not preceded by '.'.
  const isParamList = new Array(n).fill(false);
  for (let i = 0; i < n; i++) {
    if (!isP(i, '(')) continue;
    const j = match[i];
    const next = j + 1 < n ? val(j + 1) : '';
    if (next !== ':' && next !== '{' && next !== '=>') continue;
    const p = i - 1;
    if (p < 0) continue;
    if (isId(p, 'function')) {
      isParamList[i] = true;
      continue;
    }
    if (tokens[p].type !== 'id') continue;
    if (KEYWORDS.has(val(p))) continue;
    if (p - 1 >= 0 && isP(p - 1, '.')) continue;
    isParamList[i] = true;
  }

  // Mark class-body braces.
  const classBrace = new Array(n).fill(false);
  for (let i = 0; i < n; i++) {
    if (!isId(i, 'class')) continue;
    for (let j = i + 1; j < n; j++) {
      if (isP(j, '{')) {
        classBrace[j] = true;
        break;
      }
      if (isP(j, ';')) break;
    }
  }

  // Strip interface / type-alias / import-type / export-type declarations.
  for (let i = 0; i < n; i++) {
    if (dropped[i]) continue;
    if (isId(i, 'interface')) {
      const start = i > 0 && isId(i - 1, 'export') ? i - 1 : i;
      let j = i + 1;
      while (j < n && !isP(j, '{')) j++;
      if (j >= n) throw new Error(file + ': interface without body');
      drop(start, match[j]);
      continue;
    }
    if (isId(i, 'type') && i + 1 < n && tokens[i + 1].type === 'id') {
      const start = i > 0 && isId(i - 1, 'export') ? i - 1 : i;
      let j = i + 1;
      while (j < n && !isP(j, '=')) j++;
      let d = 0;
      let end = -1;
      for (let k = j; k < n; k++) {
        const v = val(k);
        if (v === '{' || v === '[' || v === '(' || v === '<') d++;
        else if (v === '}' || v === ']' || v === ')' || v === '>') d--;
        else if (v === ';' && d === 0) {
          end = k;
          break;
        }
      }
      if (end < 0) throw new Error(file + ': unterminated type alias');
      drop(start, end);
      continue;
    }
    if (isId(i, 'import') && i + 1 < n && isId(i + 1, 'type')) {
      let j = i + 1;
      while (j < n && !isP(j, ';')) j++;
      drop(i, j);
      continue;
    }
    if (isId(i, 'export') && i + 1 < n && isId(i + 1, 'type')) {
      let j = i + 1;
      while (j < n && !isP(j, ';')) j++;
      drop(i, j);
      continue;
    }
  }

  // Strip parameter annotations and return types.
  for (let i = 0; i < n; i++) {
    if (!isParamList[i]) continue;
    const j = match[i];
    let d = 1;
    for (let k = i + 1; k < j; k++) {
      const v = val(k);
      if (v === '(' || v === '[' || v === '{' || v === '<') d++;
      else if (v === ')' || v === ']' || v === '}' || v === '>') d--;
      else if (v === ':' && d === 1 && tokens[k - 1].type === 'id') {
        dropped[k] = true;
        let m = k + 1;
        let d2 = 0;
        while (m < j) {
          const w = val(m);
          if (w === '(' || w === '[' || w === '{' || w === '<') d2++;
          else if (w === ')' || w === ']' || w === '}' || w === '>') {
            if (d2 === 0) break;
            d2--;
          } else if ((w === ',' || w === '=') && d2 === 0) break;
          dropped[m] = true;
          m++;
        }
      }
    }
    if (j + 1 < n && isP(j + 1, ':')) {
      dropped[j + 1] = true;
      let m = j + 2;
      let d2 = 0;
      while (m < n) {
        const w = val(m);
        if (w === '{' && d2 === 0) break;
        if (w === '=>' && d2 === 0) break;
        if (w === '(' || w === '[' || w === '<') d2++;
        else if (w === ')' || w === ']' || w === '>') d2--;
        dropped[m] = true;
        m++;
      }
    }
  }

  // Strip variable declaration annotations: const/let/var x: T = ...
  for (let i = 0; i < n; i++) {
    if (dropped[i]) continue;
    if (!isId(i) || !['const', 'let', 'var'].includes(val(i))) continue;
    if (!(i + 2 < n && tokens[i + 1].type === 'id' && isP(i + 2, ':'))) continue;
    dropped[i + 2] = true;
    let m = i + 3;
    let d2 = 0;
    while (m < n) {
      const w = val(m);
      if ((w === '=' || w === ';' || w === ',') && d2 === 0) break;
      if (w === '(' || w === '[' || w === '{' || w === '<') d2++;
      else if (w === ')' || w === ']' || w === '}' || w === '>') d2--;
      dropped[m] = true;
      m++;
    }
  }

  // Strip class field annotations at class-body depth 1.
  for (let c = 0; c < n; c++) {
    if (!classBrace[c]) continue;
    const end = match[c];
    let d = 1;
    for (let k = c + 1; k < end; k++) {
      const v = val(k);
      if (v === '{') d++;
      else if (v === '}') d--;
      else if (d === 1 && tokens[k].type === 'id' && k + 1 < end && isP(k + 1, ':')) {
        const p = k - 1;
        const atStart = p === c || isP(p, ';') || isP(p, '}');
        if (!atStart) continue;
        dropped[k + 1] = true;
        let m = k + 2;
        let d2 = 0;
        while (m < end) {
          const w = val(m);
          if ((w === '=' || w === ';') && d2 === 0) break;
          if (w === '(' || w === '[' || w === '<' || w === '{') d2++;
          else if (w === ')' || w === ']' || w === '>' || w === '}') d2--;
          dropped[m] = true;
          m++;
        }
      }
    }
  }

  let out = '';
  for (let i = 0; i < n; i++) {
    out += tokens[i].pre;
    if (!dropped[i]) out += tokens[i].value;
  }
  return out;
}

mkdirSync(OUT, { recursive: true });
const files = readdirSync(SRC).filter((f) => f.endsWith('.ts')).sort();
if (files.length === 0) {
  throw new Error('no .ts files found in ' + SRC);
}
const written = [];
for (const f of files) {
  const src = readFileSync(join(SRC, f), 'utf8');
  const tokens = tokenize(src, f);
  const js = strip(tokens, f);
  const outName = f.replace(/\.ts$/, '.js');
  writeFileSync(join(OUT, outName), js);
  written.push(outName);
}
// Verify the emitted files parse and link by importing them.
for (const outName of written) {
  await import(pathToFileURL(join(OUT, outName)).href);
}
console.log('built ' + written.length + ' file(s) -> dist/: ' + written.join(', '));
