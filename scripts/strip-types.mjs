const IDENT_START = /[A-Za-z_$]/;
const IDENT_CHAR = /[A-Za-z0-9_$]/;

// Strips type syntax from an erasable-only subset of TypeScript so the
// sources in this repo can run on a plain Node runtime without a compiler.
// Supported: interfaces, type aliases, `import type`, `declare` statements,
// parameter/return/class-field type annotations, generics on declarations
// and call sites, `as` casts, non-null assertions, class member modifiers,
// optional-parameter `?`.
// Sources must avoid: enums, namespaces, parameter properties, object-literal
// or function types in return position, function types inside call-site
// generics, ternaries inside call arguments, regex literals, and the words
// class/interface/type/as/declare used as property names.
export function stripTypes(src) {
  const n = src.length;
  let out = '';
  let i = 0;
  const stack = [];
  let classPending = false;

  const top = () => stack[stack.length - 1];

  const prevSig = () => {
    for (let j = out.length - 1; j >= 0; j--) {
      const c = out[j];
      if (c !== ' ' && c !== '\t' && c !== '\n' && c !== '\r') return c;
    }
    return '';
  };

  const skipSpaces = (j) => {
    while (j < n && (src[j] === ' ' || src[j] === '\t' || src[j] === '\n' || src[j] === '\r')) j++;
    return j;
  };

  const scanBalanced = (j, open, close) => {
    let depth = 0;
    while (j < n) {
      if (src[j] === open) depth++;
      else if (src[j] === close) {
        depth--;
        if (depth === 0) return j + 1;
      }
      j++;
    }
    return j;
  };

  const skipToSemicolon = (j) => {
    let paren = 0;
    let brace = 0;
    let bracket = 0;
    let angle = 0;
    while (j < n) {
      const c = src[j];
      if (c === '(') paren++;
      else if (c === ')') paren = Math.max(0, paren - 1);
      else if (c === '{') brace++;
      else if (c === '}') brace = Math.max(0, brace - 1);
      else if (c === '[') bracket++;
      else if (c === ']') bracket = Math.max(0, bracket - 1);
      else if (c === '<') angle++;
      else if (c === '>') { if (angle > 0) angle--; }
      else if (c === ';' && paren === 0 && brace === 0 && bracket === 0 && angle === 0) return j + 1;
      j++;
    }
    return j;
  };

  const scanType = (j, terminators, arrowIsContent) => {
    let paren = 0;
    let brace = 0;
    let bracket = 0;
    let angle = 0;
    while (j < n) {
      const c = src[j];
      const atZero = paren === 0 && brace === 0 && bracket === 0 && angle === 0;
      if (atZero) {
        if (c === '=' && src[j + 1] === '>') {
          if (arrowIsContent) {
            j += 2;
            continue;
          }
          return j;
        }
        if (terminators.indexOf(c) !== -1) return j;
      }
      if (c === '(') paren++;
      else if (c === ')') paren = Math.max(0, paren - 1);
      else if (c === '{') brace++;
      else if (c === '}') brace = Math.max(0, brace - 1);
      else if (c === '[') bracket++;
      else if (c === ']') bracket = Math.max(0, bracket - 1);
      else if (c === '<') angle++;
      else if (c === '>') { if (angle > 0) angle--; }
      j++;
    }
    return j;
  };

  const tryGeneric = (j) => {
    let depth = 0;
    let k = j;
    while (k < n) {
      const c = src[k];
      if (c === '<') depth++;
      else if (c === '>') {
        depth--;
        if (depth === 0) {
          const content = src.slice(j, k + 1);
          if (/[(){};=]/.test(content)) return -1;
          return k + 1;
        }
      }
      k++;
    }
    return -1;
  };

  while (i < n) {
    const c = src[i];

    if (top() === '`') {
      if (c === '\\') { out += c + src[i + 1]; i += 2; continue; }
      if (c === '`') { out += c; stack.pop(); i++; continue; }
      if (c === '$' && src[i + 1] === '{') { out += '${'; stack.push('${'); i += 2; continue; }
      out += c; i++; continue;
    }

    if (c === '/' && src[i + 1] === '/') {
      while (i < n && src[i] !== '\n') { out += src[i]; i++; }
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      out += '/*'; i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) { out += src[i]; i++; }
      out += '*/'; i += 2;
      continue;
    }

    if (c === "'" || c === '"') {
      out += c; i++;
      while (i < n && src[i] !== c) {
        if (src[i] === '\\') { out += src[i] + src[i + 1]; i += 2; }
        else { out += src[i]; i++; }
      }
      if (i < n) { out += src[i]; i++; }
      continue;
    }

    if (c === '`') { out += c; stack.push('`'); i++; continue; }

    if (c === '(' || c === '[') { stack.push(c); out += c; i++; continue; }
    if (c === '{') {
      if (classPending) { stack.push('class{'); classPending = false; }
      else stack.push('{');
      out += c; i++; continue;
    }
    if (c === '}' || c === ')' || c === ']') { stack.pop(); out += c; i++; continue; }

    if (c === '?' && (top() === '(' || top() === 'class{')) {
      const k = skipSpaces(i + 1);
      if (src[k] === ':') { i = k; continue; }
    }

    if (c === ':') {
      const t = top();
      if (t === '(') { i = scanType(skipSpaces(i + 1), ',)=', true); continue; }
      if (t === 'class{') { i = scanType(skipSpaces(i + 1), '=;{\n', false); continue; }
      if (prevSig() === ')') { i = scanType(skipSpaces(i + 1), '{=;', false); continue; }
      out += c; i++; continue;
    }

    if (c === '!') {
      const prev = prevSig();
      const nx = src[i + 1];
      if (nx !== '=' && (IDENT_CHAR.test(prev) || prev === ')' || prev === ']')) { i++; continue; }
      out += c; i++; continue;
    }

    if (IDENT_START.test(c)) {
      let j = i + 1;
      while (j < n && IDENT_CHAR.test(src[j])) j++;
      const word = src.slice(i, j);
      const prev = prevSig();

      if (word === 'interface' && prev !== '.') {
        let k = skipSpaces(j);
        while (k < n && IDENT_CHAR.test(src[k])) k++;
        k = skipSpaces(k);
        if (src[k] === '<') k = scanBalanced(k, '<', '>');
        k = skipSpaces(k);
        if (src.startsWith('extends', k)) {
          k += 7;
          let angle = 0;
          while (k < n) {
            if (src[k] === '<') angle++;
            else if (src[k] === '>') { if (angle > 0) angle--; }
            else if (src[k] === '{' && angle === 0) break;
            k++;
          }
        }
        k = skipSpaces(k);
        i = scanBalanced(k, '{', '}');
        continue;
      }

      if (word === 'type' && prev !== '.') {
        let k = skipSpaces(j);
        if (IDENT_START.test(src[k] || '')) {
          let m = k + 1;
          while (m < n && IDENT_CHAR.test(src[m])) m++;
          m = skipSpaces(m);
          if (src[m] === '<') m = skipSpaces(scanBalanced(m, '<', '>'));
          if (src[m] === '=') { i = skipToSemicolon(m + 1); continue; }
        }
        out += word; i = j; continue;
      }

      if (word === 'declare' && prev !== '.') {
        i = skipToSemicolon(j);
        continue;
      }

      if (word === 'export') {
        const k = skipSpaces(j);
        let m = k;
        while (m < n && IDENT_CHAR.test(src[m])) m++;
        const next = src.slice(k, m);
        if (next === 'type' || next === 'interface') { i = j; continue; }
        out += word; i = j; continue;
      }

      if (word === 'import') {
        const k = skipSpaces(j);
        if (src.startsWith('type', k) && !IDENT_CHAR.test(src[k + 4] || '')) {
          let m = k;
          while (m < n && src[m] !== ';') m++;
          i = m + 1;
          continue;
        }
        out += word; i = j; continue;
      }

      if (word === 'as' && prev !== '.') {
        i = scanType(skipSpaces(j), ',);={}\n', false);
        continue;
      }

      if (word === 'class' && prev !== '.') {
        classPending = true;
        out += word; i = j; continue;
      }

      if (
        (word === 'private' || word === 'public' || word === 'protected' || word === 'readonly' || word === 'abstract') &&
        top() === 'class{'
      ) {
        const k = skipSpaces(j);
        const nc = src[k] || '';
        if (IDENT_START.test(nc) || nc === '#') { i = j; continue; }
      }

      out += word;
      i = j;
      if (src[i] === '<') {
        const end = tryGeneric(i);
        if (end !== -1) {
          const k = skipSpaces(end);
          const nx = src[k];
          if (nx === '(' || nx === '{' || src.startsWith('extends', k) || src.startsWith('implements', k)) {
            i = end;
          }
        }
      }
      continue;
    }

    out += c;
    i++;
  }

  return out;
}
