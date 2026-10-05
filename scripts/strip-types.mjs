// Minimal TypeScript type eraser for this repo's controlled coding style.
// It only deletes type-only syntax (annotations, interfaces, type aliases,
// generics, `as` casts, access modifiers). Runtime code is left untouched.
// Constraints assumed in src/: no enums, no namespaces, no parameter
// properties, no non-null assertions, no ternaries inside parentheses,
// no regex literals, no labeled statements.
import { readdirSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = join(root, 'src');
const outDir = join(root, 'build');

function maskSource(text) {
  const chars = text.split('');
  const n = text.length;
  const blank = (from, to) => {
    for (let k = from; k < to; k += 1) {
      if (chars[k] !== '\n') chars[k] = ' ';
    }
  };
  const scanTemplate = (start) => {
    let j = start + 1;
    while (j < n) {
      const t = text[j];
      if (t === '\\') { j += 2; continue; }
      if (t === '`') return j + 1;
      if (t === '$' && text[j + 1] === '{') {
        let depth = 1;
        j += 2;
        while (j < n && depth > 0) {
          const u = text[j];
          if (u === '{') depth += 1;
          else if (u === '}') depth -= 1;
          else if (u === "'" || u === '"') {
            let k = j + 1;
            while (k < n && text[k] !== u) { if (text[k] === '\\') k += 1; k += 1; }
            j = k;
          } else if (u === '`') {
            j = scanTemplate(j) - 1;
          }
          j += 1;
        }
        continue;
      }
      j += 1;
    }
    return j;
  };
  let i = 0;
  while (i < n) {
    const c = text[i];
    if (c === '/' && text[i + 1] === '/') {
      let j = i;
      while (j < n && text[j] !== '\n') j += 1;
      blank(i, j);
      i = j;
    } else if (c === '/' && text[i + 1] === '*') {
      let j = text.indexOf('*/', i + 2);
      j = j === -1 ? n : j + 2;
      blank(i, j);
      i = j;
    } else if (c === "'" || c === '"') {
      let j = i + 1;
      while (j < n && text[j] !== c) { if (text[j] === '\\') j += 1; j += 1; }
      blank(i, Math.min(j + 1, n));
      i = j + 1;
    } else if (c === '`') {
      const j = scanTemplate(i);
      blank(i, j);
      i = j;
    } else {
      i += 1;
    }
  }
  return chars.join('');
}

const isIdentStart = (c) => /[A-Za-z_$]/.test(c);
const isIdent = (c) => /[A-Za-z0-9_$]/.test(c);

function collectRanges(masked) {
  const ranges = [];
  const n = masked.length;
  const push = (start, end) => { if (end > start) ranges.push([start, end]); };
  const skipWs = (i) => { while (i < n && /\s/.test(masked[i])) i += 1; return i; };
  const skipInlineWs = (i) => { while (i < n && (masked[i] === ' ' || masked[i] === '\t')) i += 1; return i; };

  // Scan a type expression starting at i; stop based on terminators at depth 0.
  // Returns end index (exclusive) of the type, or -1 to abort.
  const scanType = (i, terminators, abortOnNewline, arrowEnds) => {
    let depth = 0;
    let j = i;
    while (j < n) {
      const c = masked[j];
      if (depth === 0) {
        if (c === '\n' && abortOnNewline) return -1;
        if (c === '=' && masked[j + 1] === '>' && arrowEnds) return j;
        if (c === '=' && masked[j + 1] !== '>') {
          if (terminators.has('=')) return j;
        } else if (c !== '=' && terminators.has(c)) return j;
      }
      if (c === '(' || c === '[' || c === '{' || c === '<') { depth += 1; j += 1; continue; }
      if (c === ')' || c === ']' || c === '}') {
        if (depth === 0) return -1;
        depth -= 1; j += 1; continue;
      }
      if (c === '>') { if (depth > 0) depth -= 1; j += 1; continue; }
      j += 1;
    }
    return -1;
  };

  let m;

  // T1: import type / export type statements
  const reTypeModule = /(?<![\w$.])(?:import|export)\s+type\s[^;]*;/g;
  while ((m = reTypeModule.exec(masked))) push(m.index, m.index + m[0].length);

  // T2: type aliases
  const reTypeAlias = /(?<![\w$.])(?:export\s+)?type\s+[A-Za-z_$][\w$]*\s*(?:<[^{;]*>)?\s*=/g;
  while ((m = reTypeAlias.exec(masked))) {
    const end = scanType(m.index + m[0].length, new Set([';']), false, false);
    if (end !== -1) push(m.index, end + 1);
  }

  // T3: interfaces
  const reInterface = /(?<![\w$.])(?:export\s+)?interface\s+[A-Za-z_$][\w$]*(?:\s*<[^\n{]*>)?(?:\s+extends\s+[^{\n]+?)?\s*\{/g;
  while ((m = reInterface.exec(masked))) {
    const open = m.index + m[0].length - 1;
    let depth = 1;
    let j = open + 1;
    while (j < n && depth > 0) {
      if (masked[j] === '{') depth += 1;
      else if (masked[j] === '}') depth -= 1;
      j += 1;
    }
    if (depth === 0) push(m.index, j);
  }

  // T4: generics immediately before a call/declaration paren: foo<T>(...)
  const reGeneric = /[A-Za-z_$][\w$]*\s*</g;
  while ((m = reGeneric.exec(masked))) {
    const lt = m.index + m[0].length - 1;
    let depth = 1;
    let j = lt + 1;
    let ok = true;
    while (j < n && depth > 0) {
      const c = masked[j];
      if (c === '<') depth += 1;
      else if (c === '>') depth -= 1;
      else if (c === '{' || c === '}' || c === ';' || c === '(' || c === ')' || c === '\n') { ok = false; break; }
      j += 1;
    }
    if (ok && depth === 0) {
      const next = skipInlineWs(j);
      if (masked[next] === '(') push(lt, j);
    }
  }

  // T5: return types after ')'
  for (let i = 0; i < n; i += 1) {
    if (masked[i] !== ')') continue;
    const next = skipWs(i + 1);
    if (masked[next] !== ':') continue;
    const end = scanType(next + 1, new Set(['{', ';', ',', ')', ']', '}', '=']), true, true);
    if (end !== -1 && (masked[end] === '{' || masked[end] === '=')) push(next, end);
  }

  // T6: annotations inside parenthesised groups (parameters)
  for (let i = 0; i < n; i += 1) {
    if (masked[i] !== '(') continue;
    let j = i + 1;
    let depth = 0;
    while (j < n) {
      const c = masked[j];
      if (c === '(' || c === '[' || c === '{') { depth += 1; j += 1; continue; }
      if (c === ')' || c === ']' || c === '}') {
        if (depth === 0) break;
        depth -= 1;
        if (depth === 0 && (c === '}' || c === ']')) {
          const after = skipInlineWs(j + 1);
          if (masked[after] === ':') {
            const end = scanType(after + 1, new Set([',', ')', '=']), false, false);
            if (end !== -1) { push(after, end); j = end; continue; }
          }
        }
        j += 1;
        continue;
      }
      if (depth === 0 && isIdentStart(c)) {
        let k = j + 1;
        while (k < n && isIdent(masked[k])) k += 1;
        let p = skipInlineWs(k);
        let optStart = -1;
        if (masked[p] === '?') { optStart = p; p = skipInlineWs(p + 1); }
        if (masked[p] === ':') {
          const end = scanType(p + 1, new Set([',', ')', '=']), false, false);
          if (end !== -1) {
            push(optStart === -1 ? p : optStart, end);
            j = end;
            continue;
          }
        }
        j = k;
        continue;
      }
      j += 1;
    }
  }

  // T7: variable declarations with annotations
  const reDecl = /(?<![\w$.])(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*:/g;
  while ((m = reDecl.exec(masked))) {
    const colon = m.index + m[0].length - 1;
    const end = scanType(colon + 1, new Set(['=', ';', ',']), true, false);
    if (end !== -1 && (masked[end] === '=' || masked[end] === ';')) push(colon, end);
  }

  // T8: class field annotations (line-start declarations ending with = or ;)
  const reField = /(?:^|\n)[ \t]*(?:(?:public|private|protected|readonly|abstract|override|declare|static)\s+)*(?!(?:case|default|const|let|var|return|if|for|while|switch|function|class|import|export|type|interface|new|throw|else|do|in|of)\b)[A-Za-z_$][\w$]*\s*:/g;
  while ((m = reField.exec(masked))) {
    const colon = m.index + m[0].length - 1;
    const end = scanType(colon + 1, new Set(['=', ';', ',']), true, false);
    if (end !== -1 && (masked[end] === '=' || masked[end] === ';')) push(colon, end);
  }

  // T9: access modifiers
  const reMod = /(?<![\w$.])(?:public|private|protected|readonly|abstract|override|declare)\s+(?=[A-Za-z_$])/g;
  while ((m = reMod.exec(masked))) push(m.index, m.index + m[0].length);

  // T10: `as` casts (simple types only)
  const reAs = /(?<![\w$.])as\s+/g;
  while ((m = reAs.exec(masked))) {
    let j = m.index + m[0].length;
    if (!isIdent(masked[j])) continue;
    while (j < n && /[\w$.]/.test(masked[j])) j += 1;
    let ok = true;
    while (ok) {
      ok = false;
      if (masked[j] === '<') {
        let depth = 1;
        let k = j + 1;
        while (k < n && depth > 0) {
          if (masked[k] === '<') depth += 1;
          else if (masked[k] === '>') depth -= 1;
          else if (masked[k] === '\n' || masked[k] === ';' || masked[k] === '{') { depth = -1; break; }
          k += 1;
        }
        if (depth === 0) { j = k; ok = true; }
      }
      if (masked[j] === '[' && masked[j + 1] === ']') { j += 2; ok = true; }
    }
    push(m.index, j);
  }

  // T11: implements clauses (simple identifiers only)
  const reImpl = /(?<![\w$.])implements\s+[A-Za-z_$][\w$]*(?:\s*,\s*[A-Za-z_$][\w$]*)*/g;
  while ((m = reImpl.exec(masked))) push(m.index, m.index + m[0].length);

  ranges.sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const [s, e] of ranges) {
    const last = merged[merged.length - 1];
    if (last && s <= last[1]) last[1] = Math.max(last[1], e);
    else merged.push([s, e]);
  }
  return merged;
}

export function stripTypes(source) {
  const masked = maskSource(source);
  const ranges = collectRanges(masked);
  let out = '';
  let pos = 0;
  for (const [s, e] of ranges) {
    out += source.slice(pos, s);
    pos = e;
  }
  out += source.slice(pos);
  return out;
}

async function main() {
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true });
  const files = readdirSync(srcDir).filter((f) => f.endsWith('.ts'));
  for (const file of files) {
    const source = readFileSync(join(srcDir, file), 'utf8');
    const stripped = stripTypes(source);
    const outFile = join(outDir, file.replace(/\.ts$/, '.js'));
    writeFileSync(outFile, stripped);
    console.log('stripped', file, '->', 'build/' + file.replace(/\.ts$/, '.js'));
  }
  await import(pathToFileURL(join(outDir, 'index.js')).href);
  console.log('build output loads OK');
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
