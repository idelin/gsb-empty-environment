// 受限 TypeScript 子集的类型擦除器。
// 本仓库离线环境没有 tsc/tsx，用它把 src/ 与 tests/ 转成可执行的 ESM。
// 源码约束：不使用 enum / class / 装饰器 / as 断言 / 非空断言 / 正则字面量 /
// 内联对象类型注解；类型只出现在 interface、type 别名、import type、
// export type、参数 / 返回值 / 变量声明的注解位置。

const PUNCT3 = ['...', '===', '!==', '**=', '<<=', '>>=', '&&=', '||=', '??='];
const PUNCT2 = ['=>', '?.', '??', '&&', '||', '==', '!=', '<=', '>=', '++', '--', '+=', '-=', '*=', '/=', '%=', '**', '<<', '>>'];

function scanTemplate(src, i) {
  i += 1;
  while (i < src.length) {
    const c = src[i];
    if (c === '\\') {
      i += 2;
      continue;
    }
    if (c === '`') return i + 1;
    if (c === '$' && src[i + 1] === '{') {
      i += 2;
      let depth = 1;
      while (i < src.length && depth > 0) {
        const ch = src[i];
        if (ch === '{') depth += 1;
        else if (ch === '}') depth -= 1;
        else if (ch === '`') {
          i = scanTemplate(src, i);
          continue;
        } else if (ch === "'" || ch === '"') {
          i += 1;
          while (i < src.length && src[i] !== ch) {
            if (src[i] === '\\') i += 1;
            i += 1;
          }
        }
        i += 1;
      }
      continue;
    }
    i += 1;
  }
  return i;
}

function tokenize(src) {
  const tokens = [];
  let i = 0;
  const isIdStart = (c) => /[A-Za-z_$]/.test(c);
  const isIdChar = (c) => /[\w$]/.test(c);
  while (i < src.length) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      const start = i;
      while (i < src.length && ' \t\r\n'.includes(src[i])) i += 1;
      tokens.push({ type: 'ws', start, end: i });
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      const start = i;
      while (i < src.length && src[i] !== '\n') i += 1;
      tokens.push({ type: 'comment', start, end: i });
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const start = i;
      i += 2;
      while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) i += 1;
      i += 2;
      tokens.push({ type: 'comment', start, end: i });
      continue;
    }
    if (c === "'" || c === '"') {
      const start = i;
      i += 1;
      while (i < src.length && src[i] !== c) {
        if (src[i] === '\\') i += 1;
        i += 1;
      }
      i += 1;
      tokens.push({ type: 'string', start, end: i });
      continue;
    }
    if (c === '`') {
      const start = i;
      i = scanTemplate(src, i);
      tokens.push({ type: 'template', start, end: i });
      continue;
    }
    if (/[0-9]/.test(c)) {
      const start = i;
      while (i < src.length && /[\w.]/.test(src[i])) i += 1;
      tokens.push({ type: 'number', start, end: i });
      continue;
    }
    if (isIdStart(c)) {
      const start = i;
      while (i < src.length && isIdChar(src[i])) i += 1;
      tokens.push({ type: 'id', start, end: i, value: src.slice(start, i) });
      continue;
    }
    const three = src.slice(i, i + 3);
    const two = src.slice(i, i + 2);
    if (PUNCT3.includes(three)) {
      tokens.push({ type: 'punct', start: i, end: i + 3, value: three });
      i += 3;
      continue;
    }
    if (PUNCT2.includes(two)) {
      tokens.push({ type: 'punct', start: i, end: i + 2, value: two });
      i += 2;
      continue;
    }
    tokens.push({ type: 'punct', start: i, end: i + 1, value: c });
    i += 1;
  }
  return tokens;
}

export function stripSource(source) {
  const tokens = tokenize(source);
  const sig = [];
  for (let k = 0; k < tokens.length; k += 1) {
    const t = tokens[k];
    if (t.type !== 'ws' && t.type !== 'comment') sig.push(k);
  }
  const T = (si) => tokens[sig[si]];
  const deletions = [];
  const stack = [];
  let skipUntil = -1;

  const isId = (t, v) => Boolean(t) && t.type === 'id' && t.value === v;
  const isPunct = (t, v) => Boolean(t) && t.type === 'punct' && t.value === v;

  const stripTypeAfter = (from, stopValues) => {
    let depth = 0;
    let last = from;
    for (let j = from + 1; j < sig.length; j += 1) {
      const t = T(j);
      if (t.type === 'punct') {
        const v = t.value;
        if (depth === 0 && stopValues.includes(v)) break;
        if (v === '(' || v === '[' || v === '{' || v === '<') depth += 1;
        else if (v === ')' || v === ']' || v === '}' || v === '>') depth -= 1;
      }
      last = j;
    }
    deletions.push([T(from).start, T(last).end]);
    return last;
  };

  const deleteThroughSemicolon = (from) => {
    let startIdx = from;
    const p = from > 0 ? T(from - 1) : null;
    if (isId(p, 'export') || isId(p, 'declare')) startIdx = from - 1;
    let depth = 0;
    let j = from;
    for (; j < sig.length; j += 1) {
      const t = T(j);
      if (t.type === 'punct') {
        const v = t.value;
        if (v === ';' && depth === 0) break;
        if (v === '(' || v === '[' || v === '{' || v === '<') depth += 1;
        else if (v === ')' || v === ']' || v === '}' || v === '>') depth -= 1;
      }
    }
    deletions.push([T(startIdx).start, T(j).end]);
    return j;
  };

  const deleteInterface = (from) => {
    let startIdx = from;
    const p = from > 0 ? T(from - 1) : null;
    if (isId(p, 'export') || isId(p, 'declare')) startIdx = from - 1;
    let j = from;
    while (j < sig.length && !isPunct(T(j), '{')) j += 1;
    let depth = 0;
    for (; j < sig.length; j += 1) {
      const t = T(j);
      if (isPunct(t, '{')) depth += 1;
      else if (isPunct(t, '}')) {
        depth -= 1;
        if (depth === 0) break;
      }
    }
    deletions.push([T(startIdx).start, T(j).end]);
    return j;
  };

  const handleColon = (si) => {
    const prev = si > 0 ? T(si - 1) : null;
    if (!prev) return -1;
    const top = stack[stack.length - 1];
    if (isPunct(prev, ')')) {
      return stripTypeAfter(si, ['{', '=>']);
    }
    if (top === 'paren') {
      if (isPunct(prev, '?')) {
        deletions.push([prev.start, prev.end]);
        return stripTypeAfter(si, [',', ')', '=']);
      }
      let depth = 0;
      let isTernary = false;
      let isParam = false;
      for (let k = si - 1; k >= 0; k -= 1) {
        const t = T(k);
        if (t.type === 'punct') {
          const v = t.value;
          if (v === ')' || v === ']' || v === '}') depth += 1;
          else if (v === '(' || v === '[' || v === '{') {
            if (depth === 0) {
              isParam = v === '(';
              break;
            }
            depth -= 1;
          } else if (v === ',' && depth === 0) {
            isParam = true;
            break;
          } else if (v === '?' && depth === 0) {
            isTernary = true;
            break;
          }
        }
      }
      if (isTernary || !isParam) return -1;
      return stripTypeAfter(si, [',', ')', '=']);
    }
    if (prev.type === 'id') {
      const prevPrev = si > 1 ? T(si - 2) : null;
      if (isId(prevPrev, 'const') || isId(prevPrev, 'let') || isId(prevPrev, 'var')) {
        return stripTypeAfter(si, ['=', ';', ',']);
      }
    }
    return -1;
  };

  const isStatementStart = (prev) =>
    prev === null ||
    isPunct(prev, ';') ||
    isPunct(prev, '{') ||
    isPunct(prev, '}') ||
    isId(prev, 'export') ||
    isId(prev, 'declare');

  for (let si = 0; si < sig.length; si += 1) {
    if (si <= skipUntil) continue;
    const tok = T(si);
    const prev = si > 0 ? T(si - 1) : null;

    if (tok.type === 'punct') {
      const v = tok.value;
      if (v === '(') stack.push('paren');
      else if (v === '[') stack.push('bracket');
      else if (v === '{') stack.push('brace');
      else if (v === ')' || v === ']' || v === '}') stack.pop();
      else if (v === ':') {
        const last = handleColon(si);
        if (last > skipUntil) skipUntil = last;
      }
      continue;
    }
    if (tok.type !== 'id') continue;

    if ((tok.value === 'import' || tok.value === 'export') && isId(T(si + 1), 'type')) {
      skipUntil = Math.max(skipUntil, deleteThroughSemicolon(si));
      continue;
    }
    if (tok.value === 'interface' && isStatementStart(prev)) {
      const next = T(si + 1);
      const after = T(si + 2);
      if (next && next.type === 'id' && (isPunct(after, '{') || isId(after, 'extends'))) {
        skipUntil = Math.max(skipUntil, deleteInterface(si));
      }
      continue;
    }
    if (tok.value === 'type' && isStatementStart(prev)) {
      const next = T(si + 1);
      const after = T(si + 2);
      if (next && next.type === 'id' && isPunct(after, '=')) {
        skipUntil = Math.max(skipUntil, deleteThroughSemicolon(si));
      }
      continue;
    }
  }

  const sorted = deletions.slice().sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const d of sorted) {
    const last = merged[merged.length - 1];
    if (last && d[0] <= last[1]) last[1] = Math.max(last[1], d[1]);
    else merged.push([d[0], d[1]]);
  }
  let out = '';
  let pos = 0;
  for (const [s, e] of merged) {
    out += source.slice(pos, s);
    pos = e;
  }
  out += source.slice(pos);
  return out;
}
