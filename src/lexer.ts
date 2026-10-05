export type TokenType = 'ident' | 'keyword' | 'number' | 'string' | 'punct' | 'eof';

export interface Token {
  type: TokenType;
  value: string;
  start: number;
  end: number;
}

export const KEYWORDS = new Set(['let', 'fn', 'import', 'from', 'as', 'export', 'return']);

const PUNCTS = new Set(['{', '}', '(', ')', ',', ';', '=', '+', '-', '*', '/']);

export class LexError extends Error {
  pos: number;
  constructor(message: string, pos: number) {
    super(message);
    this.name = 'LexError';
    this.pos = pos;
  }
}

export function tokenize(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const isIdentStart = (c: string) => /[A-Za-z_$]/.test(c);
  const isIdentPart = (c: string) => /[A-Za-z0-9_$]/.test(c);
  const isDigit = (c: string) => /[0-9]/.test(c);

  while (i < src.length) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\r' || c === '\n') {
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      while (i < src.length && src[i] !== '\n') i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '*') {
      const end = src.indexOf('*/', i + 2);
      if (end < 0) throw new LexError('unterminated block comment', i);
      i = end + 2;
      continue;
    }
    if (isIdentStart(c)) {
      const start = i;
      while (i < src.length && isIdentPart(src[i])) i++;
      const value = src.slice(start, i);
      tokens.push({ type: KEYWORDS.has(value) ? 'keyword' : 'ident', value, start, end: i });
      continue;
    }
    if (isDigit(c)) {
      const start = i;
      while (i < src.length && /[0-9.]/.test(src[i])) i++;
      tokens.push({ type: 'number', value: src.slice(start, i), start, end: i });
      continue;
    }
    if (c === '"' || c === "'") {
      const start = i;
      i++;
      let value = '';
      while (i < src.length && src[i] !== c) {
        if (src[i] === '\\') {
          value += src[i + 1];
          i += 2;
        } else {
          value += src[i];
          i++;
        }
      }
      if (i >= src.length) throw new LexError('unterminated string literal', start);
      i++;
      tokens.push({ type: 'string', value, start, end: i });
      continue;
    }
    if (PUNCTS.has(c)) {
      tokens.push({ type: 'punct', value: c, start: i, end: i + 1 });
      i++;
      continue;
    }
    throw new LexError(`unexpected character ${JSON.stringify(c)}`, i);
  }
  tokens.push({ type: 'eof', value: '', start: src.length, end: src.length });
  return tokens;
}
