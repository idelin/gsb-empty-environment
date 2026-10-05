#!/usr/bin/env node
/**
 * Zero-dependency TypeScript -> JavaScript transpiler for the controlled
 * subset used in src/. This environment has no tsc, so this script erases
 * type syntax and the test suite runs against the emitted JS.
 *
 * Supported: interface / type-alias declarations, `import type`, parameter
 * / return / variable / class-field annotations, access modifiers.
 * Intentionally unsupported in src/ (kept out by convention): generics,
 * `as` casts, enums, namespaces, decorators, optional `?` members,
 * destructuring/rest parameters with annotations, regex literals, and
 * template-literal expressions. Violations fail loudly here or in tests.
 */
import { readFileSync, writeFileSync, mkdirSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, dirname } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, '..', 'src');
const outDir = join(here, '..', 'build');

const MODIFIERS = new Set(['private', 'public', 'protected', 'readonly', 'abstract', 'override']);
const DECL_WORDS = new Set(['let', 'const', 'var', 'private', 'public', 'protected', 'readonly']);

function isIdentStart(ch) {
  return typeof ch === 'string' && /[A-Za-z_$]/.test(ch);
}
function isIdentChar(ch) {
  return typeof ch === 'string' && /[A-Za-z0-9_$]/.test(ch);
}

function skipString(src, i) {
  const quote = src[i];
  i += 1;
  while (i < src.length) {
    const c = src[i];
    if (c === '\\') {
      i += 2;
      continue;
    }
    if (c === quote) return i + 1;
    if (quote === '`' && c === '$' && src[i + 1] === '{') {
      throw new Error('template literal expressions are not supported in src/');
    }
    i += 1;
  }
  throw new Error('unterminated string literal');
}

export function stripTypes(src) {
  const n = src.length;
  let out = '';
  let i = 0;
  let prev = '';
  let prevPrev = '';
  const brackets = [];

  const pushTok = (tok) => {
    prevPrev = prev;
    prev = tok;
  };
  const skipLineComment = () => {
    while (i < n && src[i] !== '\n') i += 1;
  };
  const skipBlockComment = () => {
    i += 2;
    while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i += 1;
    i += 2;
  };
  const skipToSemicolon = () => {
    while (i < n) {
      const c = src[i];
      if (c === ';') {
        i += 1;
        return;
      }
      if (c === "'" || c === '"' || c === '`') {
        i = skipString(src, i);
        continue;
      }
      i += 1;
    }
  };
  const skipTypeStatement = () => {
    let depth = 0;
    let sawBrace = false;
    while (i < n) {
      const c = src[i];
      if (c === "'" || c === '"' || c === '`') {
        i = skipString(src, i);
        continue;
      }
      if (c === '/' && src[i + 1] === '/') {
        skipLineComment();
        continue;
      }
      if (c === '/' && src[i + 1] === '*') {
        skipBlockComment();
        continue;
      }
      if (c === '{') {
        depth += 1;
        sawBrace = true;
        i += 1;
        continue;
      }
      if (c === '}') {
        depth -= 1;
        i += 1;
        if (sawBrace && depth <= 0) {
          let j = i;
          while (j < n && /\s/.test(src[j])) j += 1;
          const next = src[j];
          if (next !== '|' && next !== '&' && next !== ',') return;
        }
        continue;
      }
      if (c === ';' && depth === 0) {
        i += 1;
        return;
      }
      i += 1;
    }
  };
  const skipTypeExpr = () => {
    let angle = 0;
    while (i < n) {
      const c = src[i];
      if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
        i += 1;
        continue;
      }
      if (isIdentChar(c) || c === '.' || c === '|' || c === '&' || c === '[' || c === ']') {
        i += 1;
        continue;
      }
      if (c === '<') {
        angle += 1;
        i += 1;
        continue;
      }
      if (c === '>') {
        if (angle > 0) {
          angle -= 1;
          i += 1;
          continue;
        }
        return;
      }
      if (c === "'" || c === '"' || c === '`') {
        i = skipString(src, i);
        continue;
      }
      if (c === '/' && src[i + 1] === '/') {
        skipLineComment();
        continue;
      }
      if (c === '/' && src[i + 1] === '*') {
        skipBlockComment();
        continue;
      }
      return;
    }
  };

  while (i < n) {
    const c = src[i];

    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      out += c;
      i += 1;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      const s = i;
      skipLineComment();
      out += src.slice(s, i);
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const s = i;
      skipBlockComment();
      out += src.slice(s, i);
      continue;
    }
    if (c === "'" || c === '"' || c === '`') {
      const s = i;
      i = skipString(src, i);
      out += src.slice(s, i);
      pushTok('str');
      continue;
    }
    if (isIdentStart(c)) {
      const s = i;
      while (i < n && isIdentChar(src[i])) i += 1;
      const word = src.slice(s, i);

      if (word === 'import') {
        let j = i;
        while (j < n && /\s/.test(src[j])) j += 1;
        if (src.startsWith('type', j) && !isIdentChar(src[j + 4] || '')) {
          skipToSemicolon();
          pushTok('stmt');
          continue;
        }
        out += word;
        pushTok(word);
        continue;
      }
      if (word === 'export') {
        let j = i;
        while (j < n && /\s/.test(src[j])) j += 1;
        const rest = src.slice(j);
        const isType = rest.startsWith('type') && !isIdentChar(rest[4] || '');
        const isIface = rest.startsWith('interface') && !isIdentChar(rest[9] || '');
        if (isType || isIface) {
          i = j + (isType ? 4 : 9);
          skipTypeStatement();
          pushTok('stmt');
          continue;
        }
        out += word;
        pushTok(word);
        continue;
      }
      if ((word === 'type' || word === 'interface') && prev !== '.') {
        skipTypeStatement();
        pushTok('stmt');
        continue;
      }
      if (MODIFIERS.has(word)) {
        out += ' '.repeat(word.length);
        pushTok(word);
        continue;
      }
      out += word;
      pushTok(word);
      continue;
    }
    if (c >= '0' && c <= '9') {
      const s = i;
      while (i < n && /[0-9a-zA-Z._]/.test(src[i])) i += 1;
      out += src.slice(s, i);
      pushTok('num');
      continue;
    }
    if (c === ':') {
      const top = brackets[brackets.length - 1];
      const isAnno =
        prev === ')' ||
        (isIdentStart(prev) &&
          (DECL_WORDS.has(prevPrev) || ((prevPrev === '(' || prevPrev === ',') && top === '(')));
      if (!isAnno) {
        out += c;
        i += 1;
        pushTok(':');
        continue;
      }
      i += 1;
      skipTypeExpr();
      pushTok('anno');
      continue;
    }
    if (c === '(' || c === '[' || c === '{') {
      brackets.push(c);
      out += c;
      i += 1;
      pushTok(c);
      continue;
    }
    if (c === ')' || c === ']' || c === '}') {
      brackets.pop();
      out += c;
      i += 1;
      pushTok(c);
      continue;
    }
    out += c;
    i += 1;
    pushTok(c);
  }
  return out;
}

mkdirSync(outDir, { recursive: true });
for (const entry of readdirSync(srcDir)) {
  if (!entry.endsWith('.ts')) continue;
  const src = readFileSync(join(srcDir, entry), 'utf8');
  const js = stripTypes(src);
  const outName = entry.replace(/\.ts$/, '.js');
  writeFileSync(join(outDir, outName), js);
  console.log('src/' + entry + ' -> build/' + outName);
}
