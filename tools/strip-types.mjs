/**
 * Minimal TypeScript type eraser (dev-only).
 *
 * This environment has no network access and no `tsc`, so the test runner
 * uses this to execute the TypeScript sources directly. It only supports the
 * "erasable syntax" subset that the sources in this repo are written in:
 *
 *   - interfaces and top-level `type` aliases
 *   - type annotations on variables, fields, parameters, return types
 *   - generics on classes/functions and at `new`/call sites
 *   - `import type`, `export` keywords, `private`/`readonly` modifiers
 *
 * Source conventions that keep erasure unambiguous (do not violate):
 *   - no ternary `?:`, no `switch`/`case`, no labels (a `:` outside an object
 *     literal is always a type annotation)
 *   - no `as` casts, no non-null `!`, no enums/namespaces/decorators,
 *     no parameter properties, no renaming destructures
 *   - comparison operators always spaced (`a < b`), generics never spaced
 *   - type-only imports always use `import type`
 *
 * `npm run typecheck` with a real `tsc` remains the source of truth.
 */

const ID_CHAR = /[A-Za-z0-9_$]/;
const WS = /\s/;

export function removeComments(src) {
  let out = '';
  let i = 0;
  const n = src.length;
  while (i < n) {
    const ch = src[i];
    if (ch === '/' && src[i + 1] === '/') {
      while (i < n && src[i] !== '\n') i += 1;
      continue;
    }
    if (ch === '/' && src[i + 1] === '*') {
      i += 2;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i += 1;
      i += 2;
      out += ' ';
      continue;
    }
    if (ch === "'" || ch === '"') {
      const start = i;
      i = skipString(src, i);
      out += src.slice(start, i);
      continue;
    }
    if (ch === '`') {
      const start = i;
      i = skipTemplate(src, i);
      out += src.slice(start, i);
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

function skipString(src, i) {
  const quote = src[i];
  i += 1;
  while (i < src.length) {
    if (src[i] === '\\') {
      i += 2;
      continue;
    }
    if (src[i] === quote) return i + 1;
    i += 1;
  }
  return i;
}

function skipTemplate(src, i) {
  i += 1;
  let depth = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === '\\') {
      i += 2;
      continue;
    }
    if (ch === '`' && depth === 0) return i + 1;
    if (ch === '$' && src[i + 1] === '{') {
      depth += 1;
      i += 2;
      continue;
    }
    if (ch === '}' && depth > 0) {
      depth -= 1;
      i += 1;
      continue;
    }
    i += 1;
  }
  return i;
}

export function stripTypes(input) {
  const src = removeComments(input);
  let out = '';
  let i = 0;
  const n = src.length;
  const braceStack = [];
  let afterModifier = false;

  const prevSigChar = (pos) => {
    let j = pos - 1;
    while (j >= 0 && WS.test(src[j])) j -= 1;
    return j >= 0 ? src[j] : '';
  };

  const prevSigWord = (pos) => {
    let j = pos - 1;
    while (j >= 0 && WS.test(src[j])) j -= 1;
    const end = j + 1;
    while (j >= 0 && ID_CHAR.test(src[j])) j -= 1;
    return src.slice(j + 1, end);
  };

  const skipSpaces = (pos) => {
    let j = pos;
    while (j < n && WS.test(src[j])) j += 1;
    return j;
  };

  const peekWord = (pos) => {
    let j = skipSpaces(pos);
    let w = '';
    while (j < n && ID_CHAR.test(src[j])) {
      w += src[j];
      j += 1;
    }
    return w;
  };

  const matchGeneric = (pos) => {
    let j = pos + 1;
    let depth = 1;
    while (j < n) {
      const ch = src[j];
      if (ch === '<') depth += 1;
      else if (ch === '>') {
        depth -= 1;
        if (depth === 0) return j + 1;
      } else if (
        ID_CHAR.test(ch) ||
        ch === ' ' ||
        ch === '\t' ||
        ch === '\n' ||
        ch === '\r' ||
        ch === ',' ||
        ch === '.' ||
        ch === '[' ||
        ch === ']' ||
        ch === '|' ||
        ch === '&' ||
        ch === '?'
      ) {
        // allowed inside a generic parameter list
      } else {
        return -1;
      }
      j += 1;
    }
    return -1;
  };

  const skipBalancedBraces = () => {
    let depth = 0;
    while (i < n) {
      const ch = src[i];
      if (ch === '{') depth += 1;
      else if (ch === '}') {
        depth -= 1;
        if (depth === 0) {
          i += 1;
          return;
        }
      } else if (ch === "'" || ch === '"') {
        i = skipString(src, i);
        continue;
      } else if (ch === '`') {
        i = skipTemplate(src, i);
        continue;
      }
      i += 1;
    }
  };

  const skipInterfaceRest = () => {
    while (i < n && src[i] !== '{') i += 1;
    skipBalancedBraces();
  };

  const skipTypeAliasRest = () => {
    let depth = 0;
    while (i < n) {
      const ch = src[i];
      if (ch === '<' || ch === '{' || ch === '[' || ch === '(') depth += 1;
      else if (ch === '>' || ch === '}' || ch === ']' || ch === ')') depth -= 1;
      else if (ch === "'" || ch === '"') {
        i = skipString(src, i);
        continue;
      } else if (ch === ';' && depth === 0) {
        i += 1;
        return;
      }
      i += 1;
    }
  };

  const skipToSemicolon = () => {
    while (i < n && src[i] !== ';') i += 1;
    if (i < n) i += 1;
  };

  const looksLikeTypeAlias = (pos) => {
    let j = skipSpaces(pos);
    if (j >= n || !ID_CHAR.test(src[j]) || /[0-9]/.test(src[j])) return false;
    while (j < n && ID_CHAR.test(src[j])) j += 1;
    j = skipSpaces(j);
    if (src[j] === '<') {
      const end = matchGeneric(j);
      if (end === -1) return false;
      j = skipSpaces(end);
    }
    return src[j] === '=';
  };

  const skipAnnotationType = () => {
    i = skipSpaces(i);
    let angle = 0;
    while (i < n) {
      const ch = src[i];
      if (ch === '<') {
        angle += 1;
        i += 1;
        continue;
      }
      if (ch === '>') {
        angle -= 1;
        i += 1;
        continue;
      }
      if (angle === 0 && (ch === ',' || ch === ')' || ch === '=' || ch === ';' || ch === '{')) return;
      if (ch === "'" || ch === '"') {
        i = skipString(src, i);
        continue;
      }
      i += 1;
    }
  };

  while (i < n) {
    const ch = src[i];

    if (ch === "'" || ch === '"') {
      const start = i;
      i = skipString(src, i);
      out += src.slice(start, i);
      continue;
    }
    if (ch === '`') {
      const start = i;
      i = skipTemplate(src, i);
      out += src.slice(start, i);
      continue;
    }

    if (ID_CHAR.test(ch) && !/[0-9]/.test(ch)) {
      let j = i;
      while (j < n && ID_CHAR.test(src[j])) j += 1;
      const word = src.slice(i, j);
      const prev = prevSigChar(i);
      const stmtStart = afterModifier || prev === '' || prev === ';' || prev === '}' || prev === '{';
      if (word === 'interface' && stmtStart) {
        i = j;
        skipInterfaceRest();
        afterModifier = false;
        continue;
      }
      if (word === 'type' && stmtStart && looksLikeTypeAlias(j)) {
        i = j;
        skipTypeAliasRest();
        afterModifier = false;
        continue;
      }
      if (word === 'import' && stmtStart && peekWord(j) === 'type') {
        i = j;
        skipToSemicolon();
        afterModifier = false;
        continue;
      }
      if (word === 'export' && stmtStart) {
        const next = peekWord(j);
        if (next === 'interface') {
          i = skipSpaces(j) + 'interface'.length;
          skipInterfaceRest();
          afterModifier = false;
          continue;
        }
        if (next === 'type' && looksLikeTypeAlias(skipSpaces(j) + 'type'.length)) {
          i = skipSpaces(j) + 'type'.length;
          skipTypeAliasRest();
          afterModifier = false;
          continue;
        }
        out += word;
        i = j;
        afterModifier = false;
        continue;
      }
      if (
        (word === 'private' || word === 'public' || word === 'protected' || word === 'readonly') &&
        stmtStart &&
        braceStack[braceStack.length - 1] !== 'object'
      ) {
        i = j;
        afterModifier = true;
        continue;
      }
      out += word;
      i = j;
      afterModifier = false;
      continue;
    }

    if (ch === '<' && ID_CHAR.test(prevSigChar(i))) {
      const end = matchGeneric(i);
      if (end !== -1) {
        const nx = src[skipSpaces(end)];
        if (nx === '(' || nx === '{' || nx === '=' || nx === ';' || nx === '.') {
          i = end;
          afterModifier = false;
          continue;
        }
      }
      out += ch;
      i += 1;
      afterModifier = false;
      continue;
    }

    if (ch === ':') {
      if (braceStack[braceStack.length - 1] === 'object') {
        out += ch;
        i += 1;
        continue;
      }
      const p = prevSigChar(i);
      if (ID_CHAR.test(p) || p === '?' || p === ')') {
        let k = out.length - 1;
        while (k >= 0 && WS.test(out[k])) k -= 1;
        if (out[k] === '?') out = out.slice(0, k) + out.slice(k + 1);
        i += 1;
        skipAnnotationType();
        afterModifier = false;
        continue;
      }
      out += ch;
      i += 1;
      afterModifier = false;
      continue;
    }

    if (ch === '{') {
      const p = prevSigChar(i);
      if (p === '=' || p === '(' || p === '[' || p === ',' || p === ':' || prevSigWord(i) === 'return') {
        braceStack.push('object');
      } else {
        braceStack.push('block');
      }
      out += ch;
      i += 1;
      afterModifier = false;
      continue;
    }

    if (ch === '}') {
      braceStack.pop();
      out += ch;
      i += 1;
      afterModifier = false;
      continue;
    }

    out += ch;
    i += 1;
    afterModifier = false;
  }
  return out;
}
