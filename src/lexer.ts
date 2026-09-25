import { ParseError } from './errors.ts';

export type Token =
  | { type: 'dot' } | { type: 'dotdot' }
  | { type: 'lbracket' } | { type: 'rbracket' }
  | { type: 'lparen' } | { type: 'rparen' }
  | { type: 'question' } | { type: 'bang' }
  | { type: 'at' } | { type: 'dollar' } | { type: 'star' } | { type: 'comma' }
  | { type: 'op'; value: '==' | '!=' | '<' | '<=' | '>' | '>=' }
  | { type: 'and' } | { type: 'or' }
  | { type: 'ident'; value: string }
  | { type: 'int'; value: number }
  | { type: 'string'; value: string }
  | { type: 'eof' };

const IDENT_START = /[$_\p{L}]/u;
const IDENT_PART = /[$_\p{L}\p{N}]/u;
const DIGIT = /[0-9]/;

export function tokenize(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  const fail = (msg: string, pos: number): never => {
    throw new ParseError(msg, src, pos);
  };
  while (i < src.length) {
    const c = src[i];
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') { i++; continue; }
    if (c === '.') {
      if (src[i + 1] === '.') { tokens.push({ type: 'dotdot' }); i += 2; }
      else { tokens.push({ type: 'dot' }); i += 1; }
      continue;
    }
    if (c === '[') { tokens.push({ type: 'lbracket' }); i++; continue; }
    if (c === ']') { tokens.push({ type: 'rbracket' }); i++; continue; }
    if (c === '(') { tokens.push({ type: 'lparen' }); i++; continue; }
    if (c === ')') { tokens.push({ type: 'rparen' }); i++; continue; }
    if (c === '?') { tokens.push({ type: 'question' }); i++; continue; }
    if (c === '@') { tokens.push({ type: 'at' }); i++; continue; }
    if (c === '$') { tokens.push({ type: 'dollar' }); i++; continue; }
    if (c === '*') { tokens.push({ type: 'star' }); i++; continue; }
    if (c === ',') { tokens.push({ type: 'comma' }); i++; continue; }
    if (c === '!') {
      if (src[i + 1] === '=') { tokens.push({ type: 'op', value: '!=' }); i += 2; }
      else { tokens.push({ type: 'bang' }); i += 1; }
      continue;
    }
    if (c === '=') {
      if (src[i + 1] === '=') { tokens.push({ type: 'op', value: '==' }); i += 2; continue; }
      fail(`unexpected character '=' (did you mean '==')`, i);
    }
    if (c === '<') {
      if (src[i + 1] === '=') { tokens.push({ type: 'op', value: '<=' }); i += 2; }
      else { tokens.push({ type: 'op', value: '<' }); i += 1; }
      continue;
    }
    if (c === '>') {
      if (src[i + 1] === '=') { tokens.push({ type: 'op', value: '>=' }); i += 2; }
      else { tokens.push({ type: 'op', value: '>' }); i += 1; }
      continue;
    }
    if (c === '&') {
      if (src[i + 1] === '&') { tokens.push({ type: 'and' }); i += 2; continue; }
      fail(`unexpected character '&' (did you mean '&&')`, i);
    }
    if (c === '|') {
      if (src[i + 1] === '|') { tokens.push({ type: 'or' }); i += 2; continue; }
      fail(`unexpected character '|' (did you mean '||')`, i);
    }
    if (DIGIT.test(c)) {
      let j = i;
      while (j < src.length && DIGIT.test(src[j])) j++;
      const value = Number(src.slice(i, j));
      if (!Number.isSafeInteger(value)) fail('integer literal out of safe range', i);
      tokens.push({ type: 'int', value });
      i = j;
      continue;
    }
    if (c === "'" || c === '"') {
      const quote = c;
      let j = i + 1;
      let out = '';
      while (j < src.length && src[j] !== quote) {
        if (src[j] === '\\') {
          const esc = src[j + 1];
          if (esc === 'n') out += '\n';
          else if (esc === 't') out += '\t';
          else if (esc === 'r') out += '\r';
          else if (esc === '\\' || esc === "'" || esc === '"') out += esc;
          else fail(`unsupported escape sequence '\\${esc ?? ''}'`, j);
          j += 2;
        } else {
          out += src[j];
          j += 1;
        }
      }
      if (j >= src.length) fail('unterminated string literal', i);
      tokens.push({ type: 'string', value: out });
      i = j + 1;
      continue;
    }
    if (IDENT_START.test(c)) {
      let j = i;
      while (j < src.length && IDENT_PART.test(src[j])) j++;
      tokens.push({ type: 'ident', value: src.slice(i, j) });
      i = j;
      continue;
    }
    fail(`unexpected character ${JSON.stringify(c)}`, i);
  }
  tokens.push({ type: 'eof' });
  return tokens;
}
