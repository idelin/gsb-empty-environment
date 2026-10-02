import { ValidationError } from './errors.ts';

export type TokenType = 'num' | 'str' | 'ident' | 'op' | 'punct' | 'eof';

export interface Token {
  type: TokenType;
  value: string;
  pos: number;
}

const OPERATORS = ['&&', '||', '==', '!=', '<=', '>=', '+', '-', '*', '/', '%', '<', '>', '!'];
const PUNCT = ['(', ')', ','];

export function lex(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }
    if (ch >= '0' && ch <= '9') {
      let j = i;
      while (j < source.length && /[0-9.]/.test(source[j])) j++;
      const text = source.slice(i, j);
      if (!/^\d+(\.\d+)?$/.test(text)) {
        throw new ValidationError(`malformed number literal '${text}' at position ${i}`);
      }
      tokens.push({ type: 'num', value: text, pos: i });
      i = j;
      continue;
    }
    if (ch === '"' || ch === "'") {
      let j = i + 1;
      let out = '';
      while (j < source.length && source[j] !== ch) {
        if (source[j] === '\\') {
          const next = source[j + 1];
          if (next === 'n') out += '\n';
          else if (next === 't') out += '\t';
          else if (next !== undefined) out += next;
          else throw new ValidationError(`unterminated string at position ${i}`);
          j += 2;
        } else {
          out += source[j];
          j++;
        }
      }
      if (j >= source.length) throw new ValidationError(`unterminated string at position ${i}`);
      tokens.push({ type: 'str', value: out, pos: i });
      i = j + 1;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      let j = i;
      while (j < source.length && /[A-Za-z0-9_]/.test(source[j])) j++;
      tokens.push({ type: 'ident', value: source.slice(i, j), pos: i });
      i = j;
      continue;
    }
    const two = source.slice(i, i + 2);
    if (OPERATORS.includes(two)) {
      tokens.push({ type: 'op', value: two, pos: i });
      i += 2;
      continue;
    }
    if (OPERATORS.includes(ch)) {
      tokens.push({ type: 'op', value: ch, pos: i });
      i += 1;
      continue;
    }
    if (PUNCT.includes(ch)) {
      tokens.push({ type: 'punct', value: ch, pos: i });
      i += 1;
      continue;
    }
    // Anything else (including '.', '[', ']', '{', ';', '=') is rejected outright,
    // so member access / indexing / assignment cannot even be tokenized.
    throw new ValidationError(`unexpected character '${ch}' at position ${i}`);
  }
  tokens.push({ type: 'eof', value: '', pos: source.length });
  return tokens;
}
