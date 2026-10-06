/**
 * Lightweight tokenizer for TypeScript/JavaScript source.
 * Produces enough fidelity for statement-level analysis:
 * comments, strings, template literals (with nested interpolations),
 * regex literals (best-effort heuristic), numbers, words and punctuation.
 *
 * Each token: { type, start, end, text, newlinesBefore }.
 * Types: 'comment' | 'str' | 'template' | 'regex' | 'num' | 'word' | 'punct'
 */

const SINGLE_PUNCT = new Set(['(', ')', '[', ']', '{', '}', ';', ',']);

// Words after which a `/` may begin a regex literal rather than division.
const REGEX_PREFIX_WORDS = new Set([
  'return', 'throw', 'case', 'typeof', 'instanceof', 'in', 'of', 'new',
  'delete', 'void', 'yield', 'await', 'do', 'else', 'finally',
]);

function isWordStart(ch) {
  return /[$_\p{L}]/u.test(ch);
}

function isWordPart(ch) {
  return /[$_\p{L}\p{N}]/u.test(ch);
}

function regexAllowedAfter(prev) {
  if (!prev) return true;
  if (prev.type === 'word') return REGEX_PREFIX_WORDS.has(prev.text);
  if (prev.type === 'punct') {
    // After a closer or an increment/decrement, `/` is division.
    return prev.text !== ')' && prev.text !== ']' && prev.text !== '++' && prev.text !== '--';
  }
  // After strings/numbers/regex/template: division.
  return false;
}

export function tokenize(src) {
  const tokens = [];
  const n = src.length;
  let i = 0;
  let newlines = 0;

  const push = (type, start, end) => {
    tokens.push({ type, start, end, text: src.slice(start, end), newlinesBefore: newlines });
    newlines = 0;
  };
  const prevSignificant = () => {
    for (let k = tokens.length - 1; k >= 0; k--) {
      if (tokens[k].type !== 'comment') return tokens[k];
    }
    return null;
  };

  while (i < n) {
    const c = src[i];

    if (c === ' ' || c === '\t' || c === '\r' || c === '\f' || c === '\v') { i++; continue; }
    if (c === '\n') { newlines++; i++; continue; }

    // Comments
    if (c === '/' && src[i + 1] === '/') {
      const s = i;
      while (i < n && src[i] !== '\n') i++;
      push('comment', s, i);
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const s = i;
      i += 2;
      let inner = 0;
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) {
        if (src[i] === '\n') inner++;
        i++;
      }
      i = Math.min(i + 2, n);
      push('comment', s, i);
      newlines += inner; // newlines inside block comments still count for ASI
      continue;
    }

    // Strings
    if (c === '"' || c === "'") {
      const s = i;
      i++;
      while (i < n && src[i] !== c) {
        if (src[i] === '\\') i += 2;
        else i++;
      }
      i = Math.min(i + 1, n);
      push('str', s, i);
      continue;
    }

    // Template literal, including ${ ... } interpolations (nestable).
    if (c === '`') {
      const s = i;
      i++;
      const stack = ['`'];
      while (i < n && stack.length > 0) {
        const ch = src[i];
        const top = stack[stack.length - 1];
        if (top === '`') {
          if (ch === '\\') { i += 2; continue; }
          if (ch === '`') { stack.pop(); i++; continue; }
          if (ch === '$' && src[i + 1] === '{') { stack.push('{'); i += 2; continue; }
          i++;
          continue;
        }
        // Inside an interpolation expression.
        if (ch === '{') { stack.push('{'); i++; continue; }
        if (ch === '}') { stack.pop(); i++; continue; }
        if (ch === '`') { stack.push('`'); i++; continue; }
        if (ch === '"' || ch === "'") {
          i++;
          while (i < n && src[i] !== ch) { i += src[i] === '\\' ? 2 : 1; }
          i = Math.min(i + 1, n);
          continue;
        }
        if (ch === '/' && src[i + 1] === '/') { while (i < n && src[i] !== '\n') i++; continue; }
        if (ch === '/' && src[i + 1] === '*') {
          i += 2;
          while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++;
          i = Math.min(i + 2, n);
          continue;
        }
        i++;
      }
      push('template', s, i);
      continue;
    }

    // Regex literal vs division.
    if (c === '/' && regexAllowedAfter(prevSignificant())) {
      const s = i;
      i++;
      let inClass = false;
      let ok = false;
      while (i < n) {
        const ch = src[i];
        if (ch === '\\') { i += 2; continue; }
        if (ch === '\n') break;
        if (ch === '[') inClass = true;
        else if (ch === ']') inClass = false;
        else if (ch === '/' && !inClass) { ok = true; i++; break; }
        i++;
      }
      if (ok) {
        while (i < n && isWordPart(src[i])) i++; // flags
        push('regex', s, i);
        continue;
      }
      // Not a regex after all: treat the slash as an operator below.
      i = s;
    }

    // Numbers (decimal, hex/oct/bin, exponent, bigint suffix, separators).
    if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(src[i + 1] || ''))) {
      const s = i;
      i++;
      while (i < n && /[0-9a-zA-Z_.$]/.test(src[i])) {
        // Stop `1..toString` style: a dot followed by a non-digit ends the number.
        if (src[i] === '.' && !/[0-9]/.test(src[i + 1] || '') && src[i - 1] !== '.') {
          if (!/[0-9]/.test(src[i + 1] || '')) break;
        }
        i++;
      }
      push('num', s, i);
      continue;
    }

    // Words (identifiers / keywords).
    if (isWordStart(c)) {
      const s = i;
      i++;
      while (i < n && isWordPart(src[i])) i++;
      push('word', s, i);
      continue;
    }

    // Structural single-char punctuation.
    if (SINGLE_PUNCT.has(c)) {
      push('punct', i, i + 1);
      i++;
      continue;
    }

    // Dot / spread.
    if (c === '.') {
      if (src[i + 1] === '.' && src[i + 2] === '.') { push('punct', i, i + 3); i += 3; }
      else { push('punct', i, i + 1); i++; }
      continue;
    }

    // Optional chaining.
    if (c === '?' && src[i + 1] === '.') {
      push('punct', i, i + 2);
      i += 2;
      continue;
    }

    // Operator runs.
    if (/[=+\-*%&|^!~<>?:@#]/.test(c)) {
      const s = i;
      while (i < n && /[=+\-*%&|^!~<>?:@#]/.test(src[i])) {
        if (src[i] === '?' && src[i + 1] === '.' && i > s) break;
        i++;
      }
      push('punct', s, i);
      continue;
    }

    // Anything else: emit as a lone punct so parsing can move on.
    push('punct', i, i + 1);
    i++;
  }

  return tokens;
}
