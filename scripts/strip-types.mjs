// Dev-only type stripper: converts the constrained TypeScript subset used in
// src/*.ts into plain ESM JavaScript so tests can run without a TS toolchain.
//
// Supported (and required) conventions in src/*.ts:
//   - `import type ...;` / `export type ...;` on a single line (dropped)
//   - interfaces and type aliases live only in types.ts (never stripped)
//   - type annotations only in: params, return positions, const/let/var decls
//   - no ternary `? :` directly inside parentheses
//   - no generics syntax calls, `as` casts, non-null `!`, enums, classes

const WORD_CHAR = /[A-Za-z0-9_$]/;

export function stripTypes(source) {
  const keptLines = [];
  for (const line of source.split('\n')) {
    if (/^\s*import\s+type\s/.test(line)) continue;
    if (/^\s*export\s+type\s/.test(line)) continue;
    keptLines.push(line);
  }
  const src = keptLines.join('\n');
  const n = src.length;
  let out = '';
  let i = 0;
  const stack = [];
  const tokens = [];

  const pushToken = (token) => {
    tokens.push(token);
    if (tokens.length > 4) tokens.shift();
  };

  const prevSignificant = () => {
    for (let j = out.length - 1; j >= 0; j--) {
      const c = out[j];
      if (c !== ' ' && c !== '\t' && c !== '\n' && c !== '\r') return c;
    }
    return '';
  };

  while (i < n) {
    const ch = src[i];

    if (ch === '/' && src[i + 1] === '/') {
      let j = i;
      while (j < n && src[j] !== '\n') j++;
      out += src.slice(i, j);
      i = j;
      continue;
    }
    if (ch === '/' && src[i + 1] === '*') {
      const end = src.indexOf('*/', i + 2);
      if (end === -1) throw new Error('unterminated block comment');
      out += src.slice(i, end + 2);
      i = end + 2;
      continue;
    }
    if (ch === "'" || ch === '"' || ch === '`') {
      let j = i + 1;
      while (j < n && src[j] !== ch) {
        if (src[j] === '\\') j++;
        j++;
      }
      if (j >= n) throw new Error('unterminated string literal');
      out += src.slice(i, j + 1);
      i = j + 1;
      continue;
    }
    if (WORD_CHAR.test(ch)) {
      let j = i;
      while (j < n && WORD_CHAR.test(src[j])) j++;
      pushToken(src.slice(i, j));
      out += src.slice(i, j);
      i = j;
      continue;
    }
    if (ch === '(' || ch === '[' || ch === '{') {
      stack.push(ch);
      out += ch;
      i++;
      continue;
    }
    if (ch === ')' || ch === ']' || ch === '}') {
      stack.pop();
      out += ch;
      i++;
      continue;
    }
    if (ch === ':') {
      const top = stack.length > 0 ? stack[stack.length - 1] : '';
      const t1 = tokens[tokens.length - 1];
      const t2 = tokens[tokens.length - 2];
      const isVarDecl =
        (t2 === 'const' || t2 === 'let' || t2 === 'var') && typeof t1 === 'string';
      if (top === '(' || prevSignificant() === ')' || isVarDecl) {
        i = skipAnnotation(src, i + 1);
        continue;
      }
      out += ch;
      i++;
      continue;
    }
    out += ch;
    i++;
  }
  return out;
}

function skipAnnotation(src, i) {
  const n = src.length;
  while (i < n && /\s/.test(src[i])) i++;
  let angle = 0;
  let square = 0;
  let paren = 0;
  while (i < n) {
    const d = src[i];
    if (d === '<') angle++;
    else if (d === '>') angle--;
    else if (d === '[') square++;
    else if (d === ']') square--;
    else if (d === '(') paren++;
    else if (d === ')') {
      if (angle === 0 && square === 0 && paren === 0) break;
      paren--;
    } else if (angle === 0 && square === 0 && paren === 0) {
      if (d === ',' || d === '{' || d === '=' || d === ';') break;
    }
    i++;
  }
  return i;
}
