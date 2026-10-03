import { ParseError } from './errors.ts';

export type TokenType =
  | 'number'
  | 'string'
  | 'identifier'
  | 'operator'
  | 'punct'
  | 'eof';

export interface Token {
  readonly type: TokenType;
  readonly value: string;
  readonly pos: number;
}

const OPERATORS = [
  '&&', '||', '==', '!=', '<=', '>=',
  '+', '-', '*', '/', '%', '<', '>', '!',
] as const;

const PUNCT = new Set(['(', ')', ',']);

const KEYWORDS = new Set(['true', 'false', 'null']);

export function isKeyword(word: string): boolean {
  return KEYWORDS.has(word);
}

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  while (i < source.length) {
    const ch = source[i]!;

    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }

    if (ch >= '0' && ch <= '9') {
      let j = i;
      while (j < source.length && /[0-9]/.test(source[j]!)) j++;
      if (source[j] === '.' && /[0-9]/.test(source[j + 1] ?? '')) {
        j++;
        while (j < source.length && /[0-9]/.test(source[j]!)) j++;
      }
      tokens.push({ type: 'number', value: source.slice(i, j), pos: i });
      i = j;
      continue;
    }

    if (ch === '"' || ch === "'") {
      let j = i + 1;
      let out = '';
      let closed = false;
      while (j < source.length) {
        const c = source[j]!;
        if (c === '\\') {
          const next = source[j + 1];
          if (next === undefined) break;
          out += next === 'n' ? '\n' : next === 't' ? '\t' : next;
          j += 2;
          continue;
        }
        if (c === ch) {
          closed = true;
          j++;
          break;
        }
        out += c;
        j++;
      }
      if (!closed) throw new ParseError(`Unterminated string literal at position ${i}`);
      tokens.push({ type: 'string', value: out, pos: i });
      i = j;
      continue;
    }

    if (/[A-Za-z_]/.test(ch)) {
      let j = i;
      while (j < source.length && /[A-Za-z0-9_]/.test(source[j]!)) j++;
      tokens.push({ type: 'identifier', value: source.slice(i, j), pos: i });
      i = j;
      continue;
    }

    if (PUNCT.has(ch)) {
      tokens.push({ type: 'punct', value: ch, pos: i });
      i++;
      continue;
    }

    const two = source.slice(i, i + 2);
    const one = ch;
    if ((OPERATORS as readonly string[]).includes(two)) {
      tokens.push({ type: 'operator', value: two, pos: i });
      i += 2;
      continue;
    }
    if ((OPERATORS as readonly string[]).includes(one)) {
      tokens.push({ type: 'operator', value: one, pos: i });
      i++;
      continue;
    }

    throw new ParseError(`Unexpected character '${ch}' at position ${i}`);
  }

  tokens.push({ type: 'eof', value: '', pos: source.length });
  return tokens;
}
