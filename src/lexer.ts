export type TokenKind =
  | 'ident'
  | 'number'
  | 'let'
  | 'lbrace'
  | 'rbrace'
  | 'lparen'
  | 'rparen'
  | 'eq'
  | 'semi'
  | 'plus'
  | 'star'
  | 'eof';

export interface Token {
  kind: TokenKind;
  text: string;
  start: number;
  end: number;
}

const IDENT_START = /[A-Za-z_$]/;
const IDENT_PART = /[A-Za-z0-9_$]/;
const DIGIT = /[0-9]/;

export class LexError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = 'LexError';
    this.offset = offset;
  }
}

export function lex(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }
    if (ch === '/' && source[i + 1] === '/') {
      while (i < source.length && source[i] !== '\n') i++;
      continue;
    }
    if (ch === '/' && source[i + 1] === '*') {
      const close = source.indexOf('*/', i + 2);
      if (close < 0) throw new LexError('unterminated block comment', i);
      i = close + 2;
      continue;
    }
    if (IDENT_START.test(ch)) {
      const start = i;
      while (i < source.length && IDENT_PART.test(source[i])) i++;
      const text = source.slice(start, i);
      tokens.push({ kind: text === 'let' ? 'let' : 'ident', text, start, end: i });
      continue;
    }
    if (DIGIT.test(ch)) {
      const start = i;
      while (i < source.length && DIGIT.test(source[i])) i++;
      tokens.push({ kind: 'number', text: source.slice(start, i), start, end: i });
      continue;
    }
    const single: Record<string, TokenKind> = {
      '{': 'lbrace',
      '}': 'rbrace',
      '(': 'lparen',
      ')': 'rparen',
      '=': 'eq',
      ';': 'semi',
      '+': 'plus',
      '*': 'star',
    };
    const kind = single[ch];
    if (kind) {
      tokens.push({ kind, text: ch, start: i, end: i + 1 });
      i++;
      continue;
    }
    throw new LexError(`unexpected character ${JSON.stringify(ch)}`, i);
  }
  tokens.push({ kind: 'eof', text: '', start: source.length, end: source.length });
  return tokens;
}
