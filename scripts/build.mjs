// Minimal TypeScript -> JS transpiler for the restricted, erasable-syntax-only
// subset used by this project (no tsc available in the environment).
//
// Supported / required source conventions:
//   - type annotations on variables, parameters, return types, class fields
//   - interfaces and type aliases (removed wholesale)
//   - generic type arguments with NO space after '<' (e.g. Map<string, Body>)
//   - access modifiers (public/private/protected/readonly/abstract) are dropped
// Forbidden in sources (the stripper does not handle them):
//   - ternary '?:', switch/case, labels, 'as' casts, enums, namespaces,
//     non-null '!', optional params 'x?', function types inside annotations,
//     object-destructuring with renames, regex literals, comparisons like 'a<b'
//     (always write spaces around comparison operators)
import fs from 'node:fs';
import path from 'node:path';
import url from 'node:url';
import vm from 'node:vm';

const PUNCTS = [
  '>>>=', '===', '!==', '>>>', '<<=', '>>=', '**=', '=>', '++', '--',
  '+=', '-=', '*=', '/=', '%=', '==', '!=', '<=', '>=', '&&', '||', '??',
  '?.', '<<', '>>', '**', '&=', '|=', '^=', '...',
  '+', '-', '*', '/', '%', '<', '>', '=', '!', '&', '|', '^', '~', '?',
  ':', '.', ',', ';', '(', ')', '[', ']', '{', '}', '@', '#',
];

const MODIFIERS = new Set(['public', 'private', 'protected', 'readonly', 'abstract']);

function tokenize(src, file) {
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
      let j = i + 1;
      while (j < n && src[j] !== c) { if (src[j] === '\\') j++; j++; }
      tokens.push({ t: 'str', v: src.slice(i, j + 1), start: i, end: j + 1 });
      i = j + 1; continue;
    }
    if (c === '`') {
      let j = i + 1, depth = 0;
      while (j < n) {
        const ch = src[j];
        if (ch === '\\') { j += 2; continue; }
        if (ch === '`' && depth === 0) break;
        if (ch === '$' && src[j + 1] === '{') { depth++; j += 2; continue; }
        if (ch === '}' && depth > 0) { depth--; j++; continue; }
        j++;
      }
      tokens.push({ t: 'str', v: src.slice(i, j + 1), start: i, end: j + 1 });
      i = j + 1; continue;
    }
    if (c >= '0' && c <= '9') {
      let j = i;
      while (j < n && /[0-9a-zA-Z_.]/.test(src[j])) j++;
      tokens.push({ t: 'num', v: src.slice(i, j), start: i, end: j });
      i = j; continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      let j = i;
      while (j < n && /[A-Za-z0-9_$]/.test(src[j])) j++;
      tokens.push({ t: 'id', v: src.slice(i, j), start: i, end: j });
      i = j; continue;
    }
    let matched = null;
    for (const p of PUNCTS) { if (src.startsWith(p, i)) { matched = p; break; } }
    if (matched) {
      tokens.push({ t: 'p', v: matched, start: i, end: i + matched.length });
      i += matched.length;
      continue;
    }
    throw new Error(file + ': unexpected character ' + JSON.stringify(c) + ' at offset ' + i);
  }
  return tokens;
}

function stripTypes(tokens, file) {
  const out = [];
  const braceStack = [];
  let prev = null;
  let i = 0;
  const topBrace = () => braceStack.length ? braceStack[braceStack.length - 1] : 'block';

  while (i < tokens.length) {
    const tok = tokens[i];

    // "declare ...;" statements are removed entirely
    if (tok.t === 'id' && tok.v === 'declare') {
      i++;
      while (i < tokens.length && tokens[i].v !== ';') i++;
      i++;
      prev = { t: 'p', v: ';' };
      continue;
    }

    // "import type ...;" statements are removed entirely
    if (tok.t === 'id' && tok.v === 'import' && tokens[i + 1] && tokens[i + 1].t === 'id'
        && tokens[i + 1].v === 'type') {
      i += 2;
      while (i < tokens.length && tokens[i].v !== ';') i++;
      i++;
      prev = { t: 'p', v: ';' };
      continue;
    }

    // interface Foo { ... } (also handles "export interface ...")
    if (tok.t === 'id' && tok.v === 'interface') {
      if (out.length && out[out.length - 1].v === 'export') out.pop();
      i++;
      while (i < tokens.length && tokens[i].v !== '{') i++;
      let depth = 0;
      for (; i < tokens.length; i++) {
        if (tokens[i].v === '{') depth++;
        else if (tokens[i].v === '}') { depth--; if (depth === 0) { i++; break; } }
      }
      prev = { t: 'p', v: ';' };
      continue;
    }

    // type Alias = ... ;
    if (tok.t === 'id' && tok.v === 'type' && tokens[i + 1] && tokens[i + 1].t === 'id'
        && tokens[i + 2] && tokens[i + 2].v === '=') {
      if (out.length && out[out.length - 1].v === 'export') out.pop();
      i += 3;
      let depth = 0;
      for (; i < tokens.length; i++) {
        const v = tokens[i].v;
        if (v === '{' || v === '[' || v === '(' || v === '<') depth++;
        else if (v === '}' || v === ']' || v === ')' || v === '>') depth--;
        else if (v === ';' && depth === 0) { i++; break; }
      }
      prev = { t: 'p', v: ';' };
      continue;
    }

    // drop access modifiers
    if (tok.t === 'id' && MODIFIERS.has(tok.v)) {
      i++;
      continue;
    }

    // generic type arguments: identifier immediately followed by '<...>'
    if (tok.t === 'p' && tok.v === '<' && prev && prev.t === 'id' && tok.start === prev.end) {
      let j = i + 1, depth = 1, ok = true;
      while (j < tokens.length && depth > 0) {
        const tj = tokens[j];
        if (tj.t === 'id') { j++; continue; }
        if (tj.t === 'p') {
          if (tj.v === '<') { depth++; j++; continue; }
          if (tj.v === '>') { depth--; j++; continue; }
          if (['[', ']', ',', '|', '&', '.', '?'].includes(tj.v)) { j++; continue; }
        }
        ok = false; break;
      }
      if (ok && depth === 0) { i = j; continue; }
      // fall through: emit '<' as-is
    }

    // type annotation ':' in block context
    if (tok.t === 'p' && tok.v === ':' && topBrace() === 'block' && prev
        && (prev.t === 'id' || (prev.t === 'p' && (prev.v === ')' || prev.v === ']' || prev.v === '}')))) {
      i++;
      let depth = 0;
      let first = true;
      for (; i < tokens.length; i++) {
        const t = tokens[i];
        if (t.t === 'p') {
          if (t.v === '{') { if (depth === 0 && !first) break; depth++; }
          else if (t.v === '(' || t.v === '[' || t.v === '<') depth++;
          else if (t.v === ')' || t.v === ']' || t.v === '}' || t.v === '>') { if (depth === 0) break; depth--; }
          else if ((t.v === ',' || t.v === '=' || t.v === ';' || t.v === '=>') && depth === 0) break;
        }
        first = false;
      }
      continue;
    }

    // brace classification for object literals vs blocks
    if (tok.t === 'p' && tok.v === '{') {
      const isObject = prev && (
        (prev.t === 'p' && ['(', '[', ',', '=', ':', '?', '&&', '||', '!', '...'].includes(prev.v))
        || (prev.t === 'id' && prev.v === 'return')
      );
      braceStack.push(isObject ? 'object' : 'block');
      out.push(tok); prev = tok; i++; continue;
    }
    if (tok.t === 'p' && tok.v === '}') {
      braceStack.pop();
      out.push(tok); prev = tok; i++; continue;
    }

    out.push(tok);
    prev = tok;
    i++;
  }
  return out;
}

function emit(tokens) {
  return tokens.map(t => t.v).join(' ');
}

function compileFile(srcPath, outPath) {
  const src = fs.readFileSync(srcPath, 'utf8');
  const tokens = tokenize(src, srcPath);
  const stripped = stripTypes(tokens, srcPath);
  const code = '// generated from ' + srcPath + ' -- do not edit\n' + emit(stripped) + '\n';
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  fs.writeFileSync(outPath, code);
  // syntax validation without executing the module
  new vm.SourceTextModule(code, { identifier: outPath });
}

const root = path.dirname(path.dirname(url.fileURLToPath(import.meta.url)));
const inputs = [];
for (const dir of ['src', 'test']) {
  const abs = path.join(root, dir);
  if (!fs.existsSync(abs)) continue;
  for (const f of fs.readdirSync(abs)) {
    if (f.endsWith('.ts')) inputs.push(path.join(abs, f));
  }
}
for (const srcPath of inputs) {
  const rel = path.relative(root, srcPath);
  const outPath = path.join(root, 'dist', rel.replace(/\.ts$/, '.js'));
  compileFile(srcPath, outPath);
  console.log('compiled', rel, '->', path.relative(root, outPath));
}
