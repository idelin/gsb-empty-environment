/**
 * Lightweight tokenizer for TypeScript/JavaScript source.
 *
 * It is not a full parser: it only needs to be robust enough to find
 * statement boundaries. Comments, strings, template literals and regex
 * literals are consumed as opaque trivia/tokens so that braces and
 * semicolons inside them never confuse statement segmentation.
 */

export type TokenType = 'word' | 'punct' | 'literal';

export interface Token {
  type: TokenType;
  value: string;
  start: number;
  end: number;
}

const WORD_CHAR = /[$\w]/;

const KEYWORDS_BEFORE_REGEX = new Set([
  'return', 'typeof', 'case', 'in', 'of', 'new', 'delete', 'void',
  'throw', 'else', 'do', 'instanceof', 'yield', 'await',
]);

function isWhitespace(ch: string): boolean {
  return ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n' ||
    ch === '\f' || ch === '\v' || ch === ' ';
}

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  const n = source.length;
  let i = 0;

  const prev = (): Token | undefined => tokens[tokens.length - 1];

  while (i < n) {
    const ch = source[i];

    if (isWhitespace(ch)) { i++; continue; }

    // Line comment.
    if (ch === '/' && source[i + 1] === '/') {
      i += 2;
      while (i < n && source[i] !== '\n') i++;
      continue;
    }

    // Block comment.
    if (ch === '/' && source[i + 1] === '*') {
      i += 2;
      while (i < n && !(source[i] === '*' && source[i + 1] === '/')) i++;
      i = Math.min(i + 2, n);
      continue;
    }

    // String literal.
    if (ch === '"' || ch === "'") {
      const start = i;
      i++;
      while (i < n && source[i] !== ch) {
        if (source[i] === '\\') i++;
        i++;
      }
      i = Math.min(i + 1, n);
      tokens.push({ type: 'literal', value: source.slice(start, i), start, end: i });
      continue;
    }

    // Template literal, consumed as one opaque token (handles nested ${ ... }).
    if (ch === '`') {
      const start = i;
      i++;
      let depth = 0;
      while (i < n) {
        const c = source[i];
        if (c === '\\') { i += 2; continue; }
        if (c === '`' && depth === 0) { i++; break; }
        if (c === '$' && source[i + 1] === '{') { depth++; i += 2; continue; }
        if (c === '}' && depth > 0) { depth--; i++; continue; }
        i++;
      }
      tokens.push({ type: 'literal', value: source.slice(start, i), start, end: i });
      continue;
    }

    // Regex literal (heuristic) or division punct.
    if (ch === '/') {
      const p = prev();
      const regexAllowed = !p ||
        (p.type === 'punct' && p.value !== ')' && p.value !== ']' && p.value !== '}') ||
        (p.type === 'word' && KEYWORDS_BEFORE_REGEX.has(p.value));
      if (regexAllowed) {
        const start = i;
        i++;
        let inClass = false;
        while (i < n) {
          const c = source[i];
          if (c === '\\') { i += 2; continue; }
          if (c === '[') { inClass = true; i++; continue; }
          if (c === ']') { inClass = false; i++; continue; }
          if (c === '/' && !inClass) { i++; break; }
          if (c === '\n') break;
          i++;
        }
        while (i < n && /[a-z]/i.test(source[i])) i++; // flags
        tokens.push({ type: 'literal', value: source.slice(start, i), start, end: i });
        continue;
      }
      // fall through: division operator, emitted as a plain punct below
    }

    // Identifier / keyword / number.
    if (WORD_CHAR.test(ch)) {
      const start = i;
      i++;
      while (i < n && WORD_CHAR.test(source[i])) i++;
      tokens.push({ type: 'word', value: source.slice(start, i), start, end: i });
      continue;
    }

    // `=>` is significant for block detection.
    if (ch === '=' && source[i + 1] === '>') {
      tokens.push({ type: 'punct', value: '=>', start: i, end: i + 2 });
      i += 2;
      continue;
    }

    tokens.push({ type: 'punct', value: ch, start: i, end: i + 1 });
    i++;
  }

  return tokens;
}
