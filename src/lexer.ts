export type TokenType = 'num' | 'str' | 'ident' | 'keyword' | 'punct' | 'op' | 'eof';

export interface Token {
  type: TokenType;
  value: string;
  start: number;
  end: number;
  line: number;
}

export class LexError extends Error {}

const KEYWORDS = new Set(['let', 'if', 'else', 'while', 'fn', 'return', 'true', 'false', 'null']);
const PUNCTS = new Set(['(', ')', '{', '}', ',', ';']);
const OPS = ['==', '!=', '<=', '>=', '&&', '||', '+', '-', '*', '/', '%', '<', '>', '!', '='];

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let pos = 0;
  let line = 1;

  const push = (type: TokenType, value: string, start: number, end: number, tokLine: number): void => {
    tokens.push({ type, value, start, end, line: tokLine });
  };

  while (pos < source.length) {
    const ch = source[pos];

    if (ch === '\n') {
      line++;
      pos++;
      continue;
    }
    if (ch === ' ' || ch === '\t' || ch === '\r') {
      pos++;
      continue;
    }
    if (ch === '/' && source[pos + 1] === '/') {
      while (pos < source.length && source[pos] !== '\n') pos++;
      continue;
    }
    if (ch === '/' && source[pos + 1] === '*') {
      const start = pos;
      pos += 2;
      while (pos < source.length && !(source[pos] === '*' && source[pos + 1] === '/')) {
        if (source[pos] === '\n') line++;
        pos++;
      }
      if (pos >= source.length) throw new LexError(`unterminated block comment at offset ${start}`);
      pos += 2;
      continue;
    }

    const start = pos;
    const tokLine = line;

    if (ch >= '0' && ch <= '9') {
      while (pos < source.length && source[pos] >= '0' && source[pos] <= '9') pos++;
      if (source[pos] === '.' && source[pos + 1] >= '0' && source[pos + 1] <= '9') {
        pos++;
        while (pos < source.length && source[pos] >= '0' && source[pos] <= '9') pos++;
      }
      push('num', source.slice(start, pos), start, pos, tokLine);
      continue;
    }

    if (ch === '"') {
      pos++;
      let value = '';
      while (pos < source.length && source[pos] !== '"') {
        if (source[pos] === '\n') throw new LexError(`unterminated string at line ${tokLine}`);
        if (source[pos] === '\\') {
          pos++;
          const esc = source[pos];
          if (esc === 'n') value += '\n';
          else if (esc === 't') value += '\t';
          else if (esc === '"') value += '"';
          else if (esc === '\\') value += '\\';
          else throw new LexError(`bad escape '\\${esc}' at line ${tokLine}`);
          pos++;
        } else {
          value += source[pos];
          pos++;
        }
      }
      if (pos >= source.length) throw new LexError(`unterminated string at line ${tokLine}`);
      pos++;
      push('str', value, start, pos, tokLine);
      continue;
    }

    if ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch === '_') {
      while (pos < source.length && /[A-Za-z0-9_]/.test(source[pos])) pos++;
      const word = source.slice(start, pos);
      push(KEYWORDS.has(word) ? 'keyword' : 'ident', word, start, pos, tokLine);
      continue;
    }

    if (PUNCTS.has(ch)) {
      push('punct', ch, start, start + 1, tokLine);
      pos++;
      continue;
    }

    const two = source.slice(pos, pos + 2);
    if (OPS.includes(two)) {
      push('op', two, start, start + 2, tokLine);
      pos += 2;
      continue;
    }
    if (OPS.includes(ch)) {
      push('op', ch, start, start + 1, tokLine);
      pos++;
      continue;
    }

    throw new LexError(`unexpected character '${ch}' at line ${line}`);
  }

  push('eof', '', source.length, source.length, line);
  return tokens;
}
