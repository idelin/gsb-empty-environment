// Zero-dependency transpiler for the erasable TypeScript subset used in this
// repo (type annotations, interfaces, type aliases, `import type`). It exists
// so `npm test` works offline; `npm run build` with real `tsc` remains the
// canonical build when the typescript package is available.

const MULTI_PUNCT = ['...', '=>', '?.', '??'];

function isIdStart(c) {
  return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c === '_' || c === '$';
}

function isIdChar(c) {
  return isIdStart(c) || (c >= '0' && c <= '9');
}

function isNumChar(c) {
  return (
    (c >= '0' && c <= '9') ||
    (c >= 'a' && c <= 'f') ||
    (c >= 'A' && c <= 'F') ||
    c === '.' ||
    c === '_' ||
    c === 'x' ||
    c === 'X' ||
    c === 'o' ||
    c === 'O' ||
    c === 'b' ||
    c === 'B' ||
    c === 'n'
  );
}

function scanTemplate(src, i) {
  i++;
  while (i < src.length) {
    const c = src[i];
    if (c === '\\') {
      i += 2;
      continue;
    }
    if (c === '`') return i + 1;
    if (c === '$' && src[i + 1] === '{') {
      let depth = 1;
      i += 2;
      while (i < src.length && depth > 0) {
        const d = src[i];
        if (d === '{') depth++;
        else if (d === '}') depth--;
        else if (d === '`') {
          i = scanTemplate(src, i);
          continue;
        } else if (d === "'" || d === '"') {
          const q = d;
          i++;
          while (i < src.length && src[i] !== q) {
            if (src[i] === '\\') i++;
            i++;
          }
        }
        i++;
      }
      continue;
    }
    i++;
  }
  return i;
}

export function tokenize(src) {
  const tokens = [];
  let i = 0;
  const n = src.length;
  while (i < n) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      const s = i;
      while (i < n && src[i] !== '\n') i++;
      tokens.push({ t: 'comment', s: s, e: i });
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const s = i;
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
      i = Math.min(i + 2, n);
      tokens.push({ t: 'comment', s: s, e: i });
      continue;
    }
    if (c === "'" || c === '"') {
      const s = i;
      i++;
      while (i < n && src[i] !== c) {
        if (src[i] === '\\') i++;
        i++;
      }
      i++;
      tokens.push({ t: 'string', s: s, e: i });
      continue;
    }
    if (c === '`') {
      const s = i;
      i = scanTemplate(src, i);
      tokens.push({ t: 'template', s: s, e: i });
      continue;
    }
    if (isIdStart(c)) {
      const s = i;
      while (i < n && isIdChar(src[i])) i++;
      tokens.push({ t: 'id', s: s, e: i, v: src.slice(s, i) });
      continue;
    }
    if (c >= '0' && c <= '9') {
      const s = i;
      while (i < n && isNumChar(src[i])) i++;
      tokens.push({ t: 'num', s: s, e: i });
      continue;
    }
    let matched = null;
    for (const p of MULTI_PUNCT) {
      if (src.startsWith(p, i)) {
        matched = p;
        break;
      }
    }
    const s = i;
    i += matched === null ? 1 : matched.length;
    tokens.push({ t: 'punct', s: s, e: i, v: src.slice(s, i) });
  }
  return tokens;
}

export function stripTypes(src) {
  const tokens = tokenize(src);
  const deletions = [];

  const isPunct = (tok, v) => tok !== undefined && tok.t === 'punct' && tok.v === v;
  const isId = (tok, v) => tok !== undefined && tok.t === 'id' && (v === undefined || tok.v === v);

  function findSemi(from) {
    let j = from;
    while (j < tokens.length && !isPunct(tokens[j], ';')) j++;
    return j;
  }

  // Consume a type expression starting at token index j; returns the index of
  // the first token that is NOT part of the type.
  function consumeType(j) {
    let depth = 0;
    let first = true;
    let parenFn = false;
    let k = j;
    for (; k < tokens.length; k++) {
      const tok = tokens[k];
      if (tok.t === 'punct') {
        const v = tok.v;
        if (v === '{' || v === '(' || v === '[' || v === '<') {
          if (v === '{' && depth === 0 && !first) break;
          depth++;
        } else if (v === '}' || v === ')' || v === ']' || v === '>') {
          if (depth === 0) break;
          depth--;
        } else if (depth === 0 && (v === ',' || v === '=' || v === ';' || (v === '=>' && !parenFn))) {
          break;
        }
      }
      if (first && tok.t === 'punct' && tok.v === '(') parenFn = true;
      first = false;
    }
    return k;
  }

  // Pass A: statement-level constructs (interface / type alias / import type).
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    if (tok.t !== 'id') continue;
    const prev = i > 0 ? tokens[i - 1] : undefined;

    if (tok.v === 'interface') {
      let j = i + 1;
      while (j < tokens.length && !isPunct(tokens[j], '{')) j++;
      let depth = 0;
      let end = j;
      for (; j < tokens.length; j++) {
        if (isPunct(tokens[j], '{')) depth++;
        else if (isPunct(tokens[j], '}')) {
          depth--;
          if (depth === 0) {
            end = j;
            break;
          }
        }
      }
      const startTok = isId(prev, 'export') ? prev : tok;
      deletions.push([startTok.s, tokens[end].e]);
      continue;
    }

    if (tok.v === 'import' && isId(tokens[i + 1], 'type')) {
      deletions.push([tok.s, tokens[findSemi(i + 2)].e]);
      continue;
    }

    if (tok.v === 'export' && isId(tokens[i + 1], 'type') && isPunct(tokens[i + 2], '{')) {
      deletions.push([tok.s, tokens[findSemi(i + 3)].e]);
      continue;
    }

    if (tok.v === 'type' && isId(tokens[i + 1]) && isPunct(tokens[i + 2], '=')) {
      let depth = 0;
      let j = i + 3;
      for (; j < tokens.length; j++) {
        const tj = tokens[j];
        if (tj.t === 'punct') {
          if (tj.v === '{' || tj.v === '(' || tj.v === '[' || tj.v === '<') depth++;
          else if (tj.v === '}' || tj.v === ')' || tj.v === ']' || tj.v === '>') depth--;
          else if (tj.v === ';' && depth === 0) break;
        }
      }
      const startTok = isId(prev, 'export') ? prev : tok;
      deletions.push([startTok.s, tokens[j].e]);
      continue;
    }
  }

  // Pass B: annotations (params, return types, variable/class-field declarations).
  const braceStack = [];
  const parenStack = [];
  let classPending = false;
  let declBrace = -1;
  let declParen = -1;

  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];

    if (tok.t === 'punct') {
      if (tok.v === '{') {
        braceStack.push(classPending ? 'class' : 'block');
        classPending = false;
        continue;
      }
      if (tok.v === '}') {
        braceStack.pop();
        continue;
      }
      if (tok.v === '(') {
        parenStack.push(braceStack.length);
        continue;
      }
      if (tok.v === ')') {
        parenStack.pop();
        const next = tokens[i + 1];
        if (isPunct(next, ':')) {
          const end = consumeType(i + 2);
          const terminator = end < tokens.length ? tokens[end] : undefined;
          // Only a return type when the type is followed by a body or an arrow.
          if (isPunct(terminator, '{') || isPunct(terminator, '=>')) {
            deletions.push([next.s, tokens[end - 1].e]);
          }
        }
        continue;
      }
      if (
        tok.v === ';' &&
        declBrace !== -1 &&
        braceStack.length === declBrace &&
        parenStack.length === declParen
      ) {
        declBrace = -1;
        declParen = -1;
      }
      continue;
    }

    if (tok.t !== 'id') continue;

    if (tok.v === 'class') {
      classPending = true;
      continue;
    }

    if (tok.v === 'const' || tok.v === 'let' || tok.v === 'var') {
      declBrace = braceStack.length;
      declParen = parenStack.length;
      continue;
    }

    const next = tokens[i + 1];
    let colonIndex = -1;
    let startTok = null;
    if (isPunct(next, ':')) {
      colonIndex = i + 1;
      startTok = next;
    } else if (isPunct(next, '?') && isPunct(tokens[i + 2], ':')) {
      colonIndex = i + 2;
      startTok = next;
    }
    if (colonIndex === -1) continue;

    const prev = i > 0 ? tokens[i - 1] : undefined;
    const end = consumeType(colonIndex + 1);
    const range = [startTok.s, tokens[end - 1].e];

    const inClassBody =
      braceStack.length > 0 && braceStack[braceStack.length - 1] === 'class' && parenStack.length === 0;
    if (inClassBody && (isPunct(prev, '{') || isPunct(prev, ';') || isPunct(prev, '}'))) {
      deletions.push(range);
      continue;
    }

    const inParams =
      parenStack.length > 0 && braceStack.length === parenStack[parenStack.length - 1];
    if (inParams && (isPunct(prev, '(') || isPunct(prev, ','))) {
      deletions.push(range);
      continue;
    }

    const inDecl =
      declBrace !== -1 && braceStack.length === declBrace && parenStack.length === declParen;
    if (
      inDecl &&
      (isId(prev, 'const') || isId(prev, 'let') || isId(prev, 'var') || isPunct(prev, ','))
    ) {
      deletions.push(range);
      continue;
    }
  }

  deletions.sort((a, b) => a[0] - b[0]);
  let out = '';
  let pos = 0;
  for (const d of deletions) {
    if (d[1] <= pos) continue;
    if (d[0] < pos) {
      out += src.slice(pos, d[1]);
      pos = d[1];
      continue;
    }
    out += src.slice(pos, d[0]);
    pos = d[1];
  }
  out += src.slice(pos);
  return out;
}
