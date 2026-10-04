// Dependency-free build: erases TypeScript types from src/*.ts into dist/*.js.
//
// The environment has no tsc/network, so this script compiles a controlled,
// erasable subset of TypeScript:
//   - interface / type alias declarations (removed wholesale)
//   - `import type ...` statements (removed)
//   - parameter, return-type and variable annotations (`x: T`, `): T`, `const x: T`)
//   - optional-parameter markers (`?` before `:` in parameter lists)
// Source discipline (enforced by code review, validated by the test suite
// running against the compiled output): no enums, no classes, no `as` casts,
// no explicit generic type arguments on calls/constructors, no regex literals
// in src, no object-literal types in return position, no annotations inside
// template literals.
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));
const SRC = join(root, 'src');
const DIST = join(root, 'dist');

const PUNCTS3 = ['===', '!==', '**=', '...'];
const PUNCTS2 = ['=>', '==', '!=', '<=', '>=', '&&', '||', '??', '?.', '++', '--',
  '+=', '-=', '*=', '/=', '%=', '&=', '|=', '^=', '**'];

function tokenize(src) {
  const tokens = [];
  let i = 0;
  const n = src.length;
  while (i < n) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') { i++; continue; }
    if (c === '/' && src[i + 1] === '/') { while (i < n && src[i] !== '\n') i++; continue; }
    if (c === '/' && src[i + 1] === '*') {
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
      i += 2;
      continue;
    }
    if (c === "'" || c === '"') {
      const q = c;
      let j = i + 1;
      while (j < n && src[j] !== q) { if (src[j] === '\\') j++; j++; }
      tokens.push({ t: 'str', text: src.slice(i, j + 1), start: i, end: j + 1 });
      i = j + 1;
      continue;
    }
    if (c === '`') {
      let j = i + 1;
      let depth = 0;
      while (j < n) {
        const ch = src[j];
        if (ch === '\\') { j += 2; continue; }
        if (ch === '`' && depth === 0) break;
        if (ch === '$' && src[j + 1] === '{') { depth++; j += 2; continue; }
        if (ch === '}') { if (depth > 0) depth--; j++; continue; }
        j++;
      }
      tokens.push({ t: 'str', text: src.slice(i, j + 1), start: i, end: j + 1 });
      i = j + 1;
      continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1;
      while (j < n && /[A-Za-z0-9_$]/.test(src[j])) j++;
      tokens.push({ t: 'id', text: src.slice(i, j), start: i, end: j });
      i = j;
      continue;
    }
    if (/[0-9]/.test(c)) {
      let j = i + 1;
      while (j < n && /[0-9a-fA-FxXoObB._n]/.test(src[j])) j++;
      tokens.push({ t: 'num', text: src.slice(i, j), start: i, end: j });
      i = j;
      continue;
    }
    const three = src.slice(i, i + 3);
    const two = src.slice(i, i + 2);
    if (PUNCTS3.indexOf(three) >= 0) {
      tokens.push({ t: 'punct', text: three, start: i, end: i + 3 });
      i += 3;
      continue;
    }
    if (PUNCTS2.indexOf(two) >= 0) {
      tokens.push({ t: 'punct', text: two, start: i, end: i + 2 });
      i += 2;
      continue;
    }
    tokens.push({ t: 'punct', text: c, start: i, end: i + 1 });
    i++;
  }
  return tokens;
}

function stripTypes(src) {
  const tokens = tokenize(src);
  const removals = [];
  const removedTokens = new Set();

  const markRemoval = (fromIdx, toIdx) => {
    for (let k = fromIdx; k <= toIdx; k++) removedTokens.add(k);
    removals.push([tokens[fromIdx].start, tokens[toIdx].end]);
  };

  const isStmtStart = (prev) =>
    prev === null ||
    (prev.t === 'punct' && (prev.text === ';' || prev.text === '{' || prev.text === '}')) ||
    (prev.t === 'id' && prev.text === 'export');

  // Pass A: remove import-type / interface / type-alias declarations.
  for (let i = 0; i < tokens.length; i++) {
    if (removedTokens.has(i)) continue;
    const tok = tokens[i];
    if (tok.t !== 'id') continue;
    let prev = null;
    let prevIdx = -1;
    for (let k = i - 1; k >= 0; k--) {
      if (!removedTokens.has(k)) { prev = tokens[k]; prevIdx = k; break; }
    }
    const declStart = prev && prev.t === 'id' && prev.text === 'export' ? prevIdx : i;
    if (tok.text === 'import' && isStmtStart(prev)) {
      const n1 = tokens[i + 1];
      const n2 = tokens[i + 2];
      if (n1 && n1.t === 'id' && n1.text === 'type' &&
          n2 && (n2.t === 'id' || (n2.t === 'punct' && n2.text === '{'))) {
        let j = i + 1;
        while (j < tokens.length && !(tokens[j].t === 'punct' && tokens[j].text === ';')) j++;
        markRemoval(declStart, j);
        continue;
      }
    }
    if (tok.text === 'interface' && isStmtStart(prev)) {
      let j = i + 1;
      while (j < tokens.length && !(tokens[j].t === 'punct' && tokens[j].text === '{')) j++;
      let depth = 0;
      let k = j;
      for (; k < tokens.length; k++) {
        const tk = tokens[k];
        if (tk.t === 'punct' && tk.text === '{') depth++;
        else if (tk.t === 'punct' && tk.text === '}') { depth--; if (depth === 0) break; }
      }
      markRemoval(declStart, k);
      continue;
    }
    if (tok.text === 'type' && isStmtStart(prev)) {
      const n1 = tokens[i + 1];
      if (n1 && n1.t === 'id') {
        let depth = 0;
        let j = i + 1;
        for (; j < tokens.length; j++) {
          const tk = tokens[j];
          if (tk.t === 'punct') {
            if (tk.text === '{' || tk.text === '<' || tk.text === '(' || tk.text === '[') depth++;
            else if (tk.text === '}' || tk.text === '>' || tk.text === ')' || tk.text === ']') depth--;
            else if (tk.text === ';' && depth === 0) break;
          }
        }
        markRemoval(declStart, j);
        continue;
      }
    }
  }

  // Significant token indices (not removed by pass A).
  const sig = [];
  for (let i = 0; i < tokens.length; i++) if (!removedTokens.has(i)) sig.push(i);

  // Scans a type expression starting at significant position `from`
  // (the token right after ':'). Returns the significant position of the
  // terminator, or -1 if the construct is not a type annotation.
  const scanType = (from, context) => {
    let par = 0, brk = 0, brc = 0, ang = 0;
    for (let p = from; p < sig.length; p++) {
      const tk = tokens[sig[p]];
      if (tk.t !== 'punct') continue;
      const x = tk.text;
      const depth0 = par === 0 && brk === 0 && brc === 0 && ang === 0;
      if (depth0) {
        if (context === 'var' && (x === '=' || x === ';' || x === ',')) return p;
        if (context === 'param' && (x === ',' || x === ')' || x === '=')) return p;
        if (context === 'return' && (x === '{' || x === '=>')) return p;
        if (x === ';' || x === ',' || x === ')' || x === '}') return -1;
      }
      if (x === '(') par++;
      else if (x === ')') par--;
      else if (x === '[') brk++;
      else if (x === ']') brk--;
      else if (x === '{') brc++;
      else if (x === '}') brc--;
      else if (x === '<') ang++;
      else if (x === '>') ang = Math.max(0, ang - 1);
    }
    return -1;
  };

  // Pass B: annotations and optional markers.
  const stack = [];
  let prev1 = null;
  let prev2 = null;
  for (let s = 0; s < sig.length; s++) {
    const tok = tokens[sig[s]];
    if (tok.t === 'punct') {
      if (tok.text === '?' && prev1 && prev1.t === 'id' && stack[stack.length - 1] === '(') {
        const nxt = s + 1 < sig.length ? tokens[sig[s + 1]] : null;
        if (nxt && nxt.t === 'punct' && nxt.text === ':') {
          removals.push([tok.start, tok.end]);
        }
      }
      if (tok.text === ':') {
        const top = stack[stack.length - 1];
        let context = null;
        if (prev1 && prev1.t === 'id' && prev2 && prev2.t === 'id' &&
            (prev2.text === 'const' || prev2.text === 'let' || prev2.text === 'var')) {
          context = 'var';
        } else if (prev1 && prev1.t === 'id' && prev2 && prev2.t === 'punct' &&
                   (prev2.text === '(' || prev2.text === ',') && top === '(') {
          context = 'param';
        } else if (prev1 && prev1.t === 'punct' && prev1.text === '?' && top === '(') {
          context = 'param';
        } else if (prev1 && prev1.t === 'punct' && prev1.text === ')') {
          context = 'return';
        }
        if (context) {
          const end = scanType(s + 1, context);
          if (end >= 0) removals.push([tok.start, tokens[sig[end]].start]);
        }
      }
      if (tok.text === '(' || tok.text === '[' || tok.text === '{') stack.push(tok.text);
      else if (tok.text === ')' || tok.text === ']' || tok.text === '}') stack.pop();
    }
    prev2 = prev1;
    prev1 = tok;
  }

  // Apply removals.
  removals.sort((a, b) => a[0] - b[0]);
  let out = '';
  let pos = 0;
  for (const [rs, re] of removals) {
    if (rs < pos) continue;
    out += src.slice(pos, rs);
    pos = re;
  }
  out += src.slice(pos);

  // Rewrite relative module specifiers to compiled .js files.
  const fixPath = (m, a, p, b) => a + (p.endsWith('.js') ? p : p + '.js') + b;
  out = out.replace(/(from\s+['"])(\.\.?\/[^'"]+?)(['"])/g, fixPath);
  out = out.replace(/(import\s+['"])(\.\.?\/[^'"]+?)(['"])/g, fixPath);
  return out;
}

mkdirSync(DIST, { recursive: true });
const files = readdirSync(SRC).filter((f) => f.endsWith('.ts')).sort();
for (const f of files) {
  const src = readFileSync(join(SRC, f), 'utf8');
  let js = stripTypes(src);
  if (!/export|import/.test(js)) js += '\nexport {};\n';
  writeFileSync(join(DIST, f.replace(/\.ts$/, '.js')), js);
  console.log('built dist/' + f.replace(/\.ts$/, '.js'));
}

// Smoke check: the compiled entry point must load.
await import(pathToFileURL(join(DIST, 'index.js')).href);
console.log('build ok');
