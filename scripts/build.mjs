// Offline, zero-dependency build: strips erasable TypeScript syntax from
// src/*.ts into runnable ESM in dist/. The sources intentionally use only a
// disciplined subset of TypeScript (no enums, namespaces, decorators,
// parameter properties, `as` casts, non-null assertions, optional params,
// or call-site generics), which this script erases. When the `typescript`
// package is available you can equivalently run `npm run build:tsc`.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const MULTI_PUNCT = [
  '>>>=', '===', '!==', '>>>', '<<=', '>>=', '**=', '&&=', '||=', '??=',
  '=>', '==', '!=', '<=', '>=', '&&', '||', '??', '?.', '++', '--', '+=',
  '-=', '*=', '/=', '%=', '<<', '>>', '**', '...', '&=', '|=', '^=',
];

const PARAM_BLOCKLIST = new Set([
  'if', 'for', 'while', 'switch', 'catch', 'with', 'return', 'typeof',
  'instanceof', 'in', 'of', 'new', 'delete', 'void', 'throw', 'else', 'do',
  'case', 'yield', 'await', 'class', 'extends', 'super', 'this', 'import',
  'export', 'default', 'const', 'let', 'var',
]);

const CLASS_MODIFIERS = new Set([
  'public', 'private', 'protected', 'readonly', 'abstract', 'declare', 'override',
]);

function tokenize(src) {
  const tokens = [];
  let i = 0;
  const n = src.length;
  while (i < n) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      let j = i + 1;
      while (j < n && (src[j] === ' ' || src[j] === '\t' || src[j] === '\r' || src[j] === '\n')) j++;
      i = j;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      let j = i;
      while (j < n && src[j] !== '\n') j++;
      tokens.push({ t: 'comment', s: i, e: j });
      i = j;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      let j = src.indexOf('*/', i + 2);
      if (j === -1) throw new Error('unterminated block comment');
      j += 2;
      tokens.push({ t: 'comment', s: i, e: j });
      i = j;
      continue;
    }
    if (c === "'" || c === '"' || c === '`') {
      let j = i + 1;
      while (j < n) {
        if (src[j] === '\\') { j += 2; continue; }
        if (src[j] === c) { j++; break; }
        if (c !== '`' && src[j] === '\n') throw new Error('unterminated string');
        j++;
      }
      if (j > n) throw new Error('unterminated string');
      tokens.push({ t: 'string', s: i, e: j });
      i = j;
      continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_$]/.test(src[j])) j++;
      tokens.push({ t: 'id', s: i, e: j, v: src.slice(i, j) });
      i = j;
      continue;
    }
    if (/[0-9]/.test(c)) {
      let j = i + 1;
      while (j < n && /[0-9a-zA-Z._]/.test(src[j])) j++;
      tokens.push({ t: 'num', s: i, e: j });
      i = j;
      continue;
    }
    let matched = null;
    for (const op of MULTI_PUNCT) {
      if (src.startsWith(op, i)) { matched = op; break; }
    }
    if (matched !== null) {
      tokens.push({ t: 'punct', s: i, e: i + matched.length, v: matched });
      i += matched.length;
      continue;
    }
    tokens.push({ t: 'punct', s: i, e: i + 1, v: c });
    i += 1;
  }
  return tokens;
}

// Nested generic closings like `Map<T, Set<T>>` lex as shift operators.
// The supported subset has no shift expressions, so split them back.
function splitShifts(tokens) {
  const out = [];
  for (const tk of tokens) {
    if (tk.t === 'punct' && (tk.v === '>>' || tk.v === '>>>')) {
      for (let k = 0; k < tk.v.length; k++) {
        out.push({ t: 'punct', s: tk.s + k, e: tk.s + k + 1, v: '>' });
      }
    } else {
      out.push(tk);
    }
  }
  return out;
}

function buildBracketMap(tokens) {
  const match = new Map();
  const stack = [];
  const open = { '(': ')', '[': ']', '{': '}' };
  const close = { ')': true, ']': true, '}': true };
  tokens.forEach((tk, idx) => {
    if (tk.t !== 'punct') return;
    if (open[tk.v] !== undefined) stack.push(idx);
    else if (close[tk.v] === true) {
      const o = stack.pop();
      if (o === undefined) throw new Error('unbalanced bracket ' + tk.v);
      match.set(o, idx);
      match.set(idx, o);
    }
  });
  if (stack.length > 0) throw new Error('unbalanced brackets');
  return match;
}

function isPunct(tk, v) {
  return tk !== undefined && tk.t === 'punct' && tk.v === v;
}

function isId(tk, v) {
  return tk !== undefined && tk.t === 'id' && (v === undefined || tk.v === v);
}

// Scans a type expression starting at token index `from` until a terminator
// punctuator (one of `stopAt`) appears at nesting depth 0. Returns the index
// of the terminator. Nesting tracks () [] {} and <>.
function scanType(tokens, from, stopAt) {
  let depth = 0;
  for (let j = from; j < tokens.length; j++) {
    const tk = tokens[j];
    if (tk.t === 'punct') {
      if (tk.v === '(' || tk.v === '[' || tk.v === '{' || tk.v === '<') depth++;
      else if (tk.v === ')' || tk.v === ']' || tk.v === '}' || tk.v === '>') {
        if (depth === 0 && stopAt.includes(tk.v)) return j;
        depth--;
      } else if (depth === 0 && stopAt.includes(tk.v)) return j;
    }
  }
  throw new Error('unterminated type annotation');
}

function stripTypes(src) {
  const all = splitShifts(tokenize(src));
  const removals = [];
  const markRange = (fromIdx, toIdx, tokens) => {
    removals.push([tokens[fromIdx].s, tokens[toIdx].e]);
  };

  // Pass 1+2: drop `interface`/`type` declarations and generic parameter
  // lists, then compact the token stream for the annotation passes.
  const live1 = [];
  {
    const tokens = all.filter((tk) => tk.t === 'id' || tk.t === 'punct' || tk.t === 'string' || tk.t === 'num');
    const match = buildBracketMap(tokens);
    const dead = new Set();
    for (let i = 0; i < tokens.length; i++) {
      const tk = tokens[i];
      if (!isId(tk)) continue;
      const prev = tokens[i - 1];
      if (isPunct(prev, '.')) continue;
      if (tk.v === 'interface') {
        let j = i + 1;
        while (j < tokens.length && !isPunct(tokens[j], '{')) j++;
        const end = match.get(j);
        if (end === undefined) throw new Error('bad interface declaration');
        const start = isId(prev, 'export') || isId(prev, 'declare') ? i - 1 : i;
        for (let k = start; k <= end; k++) dead.add(k);
        markRange(start, end, tokens);
      } else if (tk.v === 'type' && isId(tokens[i + 1])) {
        let j = i + 1;
        let depth = 0;
        while (j < tokens.length) {
          const t2 = tokens[j];
          if (t2.t === 'punct') {
            if (t2.v === ';' && depth === 0) break;
            if (t2.v === '(' || t2.v === '[' || t2.v === '{' || t2.v === '<') depth++;
            else if (t2.v === ')' || t2.v === ']' || t2.v === '}' || t2.v === '>') depth--;
          }
          j++;
        }
        if (j >= tokens.length) throw new Error('bad type alias');
        const start = isId(prev, 'export') || isId(prev, 'declare') ? i - 1 : i;
        for (let k = start; k <= j; k++) dead.add(k);
        markRange(start, j, tokens);
      }
    }
    // Generic parameter lists: id<...> followed by `(` or `{`.
    for (let i = 0; i < tokens.length; i++) {
      if (dead.has(i) || !isPunct(tokens[i], '<')) continue;
      const prev = tokens[i - 1];
      if (!isId(prev)) continue;
      let depth = 0;
      let j = i;
      let ok = false;
      for (; j < tokens.length; j++) {
        const t2 = tokens[j];
        if (isPunct(t2, '<')) depth++;
        else if (isPunct(t2, '>')) {
          depth--;
          if (depth === 0) { ok = true; break; }
        } else if (isPunct(t2, '(') || isPunct(t2, '{') || isPunct(t2, ';') || isPunct(t2, ')')) {
          break;
        }
      }
      if (!ok) continue;
      const next = tokens[j + 1];
      if (isPunct(next, '(') || isPunct(next, '{')) {
        for (let k = i; k <= j; k++) dead.add(k);
        markRange(i, j, tokens);
      }
    }
    for (let i = 0; i < tokens.length; i++) {
      if (!dead.has(i)) live1.push(tokens[i]);
    }
  }

  // Pass 3: annotations (params, return types, class fields, variable
  // declarations) and class-level modifiers on the compacted stream.
  {
    const tokens = live1;
    const match = buildBracketMap(tokens);

    // Class body regions.
    const classDepth = new Array(tokens.length).fill(-1);
    for (let i = 0; i < tokens.length; i++) {
      if (!isId(tokens[i], 'class')) continue;
      let j = i + 1;
      while (j < tokens.length && !isPunct(tokens[j], '{')) j++;
      const open = j;
      const close = match.get(open);
      if (close === undefined) throw new Error('bad class body');
      let depth = 0;
      for (let k = open + 1; k < close; k++) {
        classDepth[k] = depth;
        if (isPunct(tokens[k], '{')) depth++;
        else if (isPunct(tokens[k], '}')) depth--;
      }
      i = close;
    }

    // Class modifiers and field annotations at class-body depth 0.
    for (let i = 0; i < tokens.length; i++) {
      if (classDepth[i] !== 0) continue;
      const tk = tokens[i];
      if (isPunct(tk, '(')) {
        i = match.get(i);
        continue;
      }
      if (isId(tk) && CLASS_MODIFIERS.has(tk.v) && isId(tokens[i + 1])) {
        removals.push([tk.s, tk.e]);
        continue;
      }
      if (isId(tk) && isPunct(tokens[i + 1], ':')) {
        const end = scanType(tokens, i + 2, ['=', ';']);
        removals.push([tokens[i + 1].s, tokens[end - 1].e]);
      }
    }

    // Parameter-list and return-type annotations.
    for (let i = 0; i < tokens.length; i++) {
      if (!isPunct(tokens[i], '(')) continue;
      const prev = tokens[i - 1];
      const close = match.get(i);
      const after = tokens[close + 1];
      let isParams = false;
      if (isId(prev) && (!PARAM_BLOCKLIST.has(prev.v) || prev.v === 'function')) isParams = true;
      else if (isPunct(after, '=>')) isParams = true;
      if (!isParams) continue;

      // Annotations inside the parameter list.
      let depth = 0;
      let ternary = 0;
      for (let j = i + 1; j < close; j++) {
        const tk = tokens[j];
        if (tk.t !== 'punct') continue;
        if (tk.v === '(' || tk.v === '[' || tk.v === '{' || tk.v === '<') { depth++; continue; }
        if (tk.v === ')' || tk.v === ']' || tk.v === '}' || tk.v === '>') { depth--; continue; }
        if (depth !== 0) continue;
        if (tk.v === '?') { ternary++; continue; }
        if (tk.v === ':') {
          if (ternary > 0) { ternary--; continue; }
          const end = scanType(tokens, j + 1, [',', ')']);
          removals.push([tk.s, tokens[end - 1].e]);
          j = end - 1;
        }
      }

      // Return type annotation.
      if (isPunct(after, ':')) {
        const endIdx = (() => {
          let d = 0;
          for (let j = close + 2; j < tokens.length; j++) {
            const tk = tokens[j];
            if (tk.t !== 'punct') continue;
            if (tk.v === '(' || tk.v === '[' || tk.v === '{' || tk.v === '<') {
              if (tk.v === '{' && d === 0) return j;
              d++;
              continue;
            }
            if (tk.v === ')' || tk.v === ']' || tk.v === '}' || tk.v === '>') { d--; continue; }
            if (d === 0 && tk.v === '=>') return j;
            if (d === 0 && (tk.v === ';' || tk.v === ',')) return -1;
          }
          return -1;
        })();
        if (endIdx !== -1) removals.push([after.s, tokens[endIdx - 1].e]);
      }
    }

    // Variable declaration annotations: const|let|var id : Type =
    for (let i = 0; i + 2 < tokens.length; i++) {
      const tk = tokens[i];
      if (!isId(tk) || (tk.v !== 'const' && tk.v !== 'let' && tk.v !== 'var')) continue;
      if (!isId(tokens[i + 1]) || !isPunct(tokens[i + 2], ':')) continue;
      const end = scanType(tokens, i + 3, ['=', ';', ',']);
      removals.push([tokens[i + 2].s, tokens[end - 1].e]);
    }
  }

  removals.sort((a, b) => a[0] - b[0]);
  let out = '';
  let pos = 0;
  for (const [s, e] of removals) {
    if (s < pos) continue;
    out += src.slice(pos, s);
    pos = e;
  }
  out += src.slice(pos);
  return out;
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const files = ['dynamic-transitive-closure'];
mkdirSync(join(root, 'dist'), { recursive: true });
for (const name of files) {
  const src = readFileSync(join(root, 'src', name + '.ts'), 'utf8');
  const js = stripTypes(src);
  writeFileSync(
    join(root, 'dist', name + '.js'),
    '// Generated from src/' + name + '.ts by scripts/build.mjs. Do not edit.\n' + js
  );
  console.log('built dist/' + name + '.js');
}
