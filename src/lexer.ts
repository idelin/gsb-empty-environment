import { JsonPathError } from './errors.ts';

export type TokenType =
  | 'number'
  | 'string'
  | 'ident'
  | 'variable'
  | 'root'
  | 'current'
  | 'punct'
  | 'eof';

export interface Token {
  readonly type: TokenType;
  readonly value: string;
  readonly pos: number;
}

const IDENT_START = /[A-Za-z_]/;
const IDENT_PART = /[A-Za-z0-9_]/;

const TWO_CHAR_OPS = new Set(['==', '!=', '<=', '>=', '&&', '||', '**']);
const ONE_CHAR_OPS = new Set(['+', '-', '*', '/', '%', '<', '>', '!', '?', '(', ')', '.', '[', ']', ',']);

export function tokenize(input: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const n = input.length;

  const skipWhitespace = () => {
    while (i < n && /\s/.test(input[i])) i += 1;
  };

  const readQuotedString = (quote: string): string => {
    const start = i;
    i += 1;
    let out = '';
    while (i < n) {
      const ch = input[i];
      if (ch === quote) {
        i += 1;
        return out;
      }
      if (ch === '\\') {
        const next = input[i + 1];
        if (next === undefined) break;
        if (next === 'u') {
          const hex = input.slice(i + 2, i + 6);
          if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
            throw new JsonPathError('JSON_PATH_SYNTAX', 'invalid unicode escape', i + 1);
          }
          out += String.fromCharCode(parseInt(hex, 16));
          i += 6;
        } else {
          const escapes: Record<string, string> = {
            '"': '"',
            "'": "'",
            '\\': '\\',
            '/': '/',
            b: '\b',
            f: '\f',
            n: '\n',
            r: '\r',
            t: '\t',
          };
          if (!(next in escapes)) {
            throw new JsonPathError('JSON_PATH_SYNTAX', `invalid escape \\${next}`, i);
          }
          out += escapes[next];
          i += 2;
        }
      } else {
        out += ch;
        i += 1;
      }
    }
    throw new JsonPathError('JSON_PATH_SYNTAX', 'unterminated string literal', start);
  };

  const readNumber = (): string => {
    const start = i;
    if (input[i] === '-') i += 1;
    while (i < n && /[0-9]/.test(input[i])) i += 1;
    if (input[i] === '.') {
      i += 1;
      while (i < n && /[0-9]/.test(input[i])) i += 1;
    }
    if (input[i] === 'e' || input[i] === 'E') {
      i += 1;
      if (input[i] === '+' || input[i] === '-') i += 1;
      while (i < n && /[0-9]/.test(input[i])) i += 1;
    }
    return input.slice(start, i);
  };

  const readIdent = (): string => {
    const start = i;
    i += 1;
    while (i < n && IDENT_PART.test(input[i])) i += 1;
    return input.slice(start, i);
  };

  while (i < n) {
    skipWhitespace();
    if (i >= n) break;
    const ch = input[i];
    const pos = i;

    if (ch === '"' || ch === "'") {
      tokens.push({ type: 'string', value: readQuotedString(ch), pos });
      continue;
    }
    if (ch === '$') {
      i += 1;
      if (i < n && IDENT_START.test(input[i])) {
        tokens.push({ type: 'variable', value: readIdent(), pos });
      } else {
        tokens.push({ type: 'root', value: '$', pos });
      }
      continue;
    }
    if (ch === '@') {
      tokens.push({ type: 'current', value: '@', pos });
      i += 1;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      const raw = readNumber();
      if (!/^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$/.test(raw)) {
        throw new JsonPathError('JSON_PATH_SYNTAX', `invalid numeric literal ${raw}`, pos);
      }
      tokens.push({ type: 'number', value: raw, pos });
      continue;
    }
    if (IDENT_START.test(ch)) {
      tokens.push({ type: 'ident', value: readIdent(), pos });
      continue;
    }
    const two = input.slice(i, i + 2);
    if (TWO_CHAR_OPS.has(two)) {
      tokens.push({ type: 'punct', value: two, pos });
      i += 2;
      continue;
    }
    if (ONE_CHAR_OPS.has(ch)) {
      tokens.push({ type: 'punct', value: ch, pos });
      i += 1;
      continue;
    }
    throw new JsonPathError('JSON_PATH_SYNTAX', `unexpected character ${JSON.stringify(ch)}`, pos);
  }

  tokens.push({ type: 'eof', value: '', pos: n });
  return tokens;
}
