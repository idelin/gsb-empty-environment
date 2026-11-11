import { QueryStructureError } from '../errors.ts';

export type TokenType =
  | 'word'
  | 'quoted'
  | 'param'
  | 'probe'
  | 'number'
  | 'punct';

export interface Token {
  type: TokenType;
  value: string;
}

const PUNCTUATION = new Set([
  '(',
  ')',
  ',',
  '.',
  '*',
  '=',
  '<',
  '>',
  '<=',
  '>=',
  '<>',
  '-',
  '+',
  '/',
]);

export function tokenize(sql: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;

  while (index < sql.length) {
    const char = sql[index];
    if (/\s/.test(char)) {
      index += 1;
      continue;
    }
    if (char === "'") {
      throw new QueryStructureError(
        'inline string literals are forbidden: all user values must use typed parameters',
      );
    }
    if (char === '"') {
      const start = index;
      index += 1;
      let value = '';
      let closed = false;
      while (index < sql.length) {
        if (sql[index] === '"') {
          if (sql[index + 1] === '"') {
            value += '"';
            index += 2;
            continue;
          }
          index += 1;
          closed = true;
          break;
        }
        value += sql[index];
        index += 1;
      }
      if (!closed) {
        throw new QueryStructureError(`unterminated quoted identifier at offset ${start}`);
      }
      tokens.push({ type: 'quoted', value });
      continue;
    }
    if (char === '$') {
      if (sql[index + 1] === '$') {
        const match = /^\$\$([^$]*)\$\$/.exec(sql.slice(index));
        if (!match) {
          throw new QueryStructureError(`unterminated probe literal at offset ${index}`);
        }
        tokens.push({ type: 'probe', value: match[1] });
        index += match[0].length;
        continue;
      }
      let end = index + 1;
      while (end < sql.length && /[0-9]/.test(sql[end])) {
        end += 1;
      }
      if (end === index + 1) {
        throw new QueryStructureError(`malformed parameter placeholder at offset ${index}`);
      }
      tokens.push({ type: 'param', value: sql.slice(index + 1, end) });
      index = end;
      continue;
    }
    if (/[0-9]/.test(char)) {
      let end = index;
      while (end < sql.length && /[0-9]/.test(sql[end])) {
        end += 1;
      }
      tokens.push({ type: 'number', value: sql.slice(index, end) });
      index = end;
      continue;
    }
    if (/[A-Za-z_]/.test(char)) {
      let end = index;
      while (end < sql.length && /[A-Za-z0-9_]/.test(sql[end])) {
        end += 1;
      }
      tokens.push({ type: 'word', value: sql.slice(index, end) });
      index = end;
      continue;
    }
    const two = sql.slice(index, index + 2);
    if (two === '<=' || two === '>=' || two === '<>') {
      tokens.push({ type: 'punct', value: two });
      index += 2;
      continue;
    }
    if (PUNCTUATION.has(char)) {
      tokens.push({ type: 'punct', value: char });
      index += 1;
      continue;
    }
    throw new QueryStructureError(`unexpected character ${char} at offset ${index}`);
  }

  return tokens;
}
