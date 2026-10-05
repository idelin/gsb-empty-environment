/**
 * Lexer for a small statement-oriented language used to exercise scope
 * binding. Operates entirely in Unicode scalar offsets.
 *
 *   program    := statement*
 *   statement  := "let" IDENT "=" expr ";"
 *               | "fn" IDENT "(" params ")" "{" statement* "}"
 *               | "{" statement* "}"
 *               | expr ";"
 *   expr       := primary (binop primary)*
 *   primary    := IDENT | NUMBER | STRING | "(" expr ")" | primary "(" args ")"
 */

import { ScalarText } from './scalar-text.ts';

export type LexKind = 'ident' | 'keyword' | 'number' | 'string' | 'punct' | 'eof';

export interface LexToken {
  kind: LexKind;
  value: string;
  /** Scalar offset, inclusive. */
  start: number;
  /** Scalar offset, exclusive. */
  end: number;
}

const KEYWORDS = new Set(['let', 'fn']);
const PUNCTS = new Set(['{', '}', '(', ')', '=', ';', ',', '+', '-', '*', '/']);

const ID_START = /\p{ID_Start}/u;
const ID_CONTINUE = /\p{ID_Continue}/u;

function isIdStart(cp: number): boolean {
  const ch = String.fromCodePoint(cp);
  return ch === '_' || ch === '$' || ID_START.test(ch);
}

function isIdContinue(cp: number): boolean {
  const ch = String.fromCodePoint(cp);
  return ch === '_' || ch === '$' || ID_CONTINUE.test(ch);
}

function isDigit(cp: number): boolean {
  return cp >= 0x30 && cp <= 0x39;
}

function isWhitespace(cp: number): boolean {
  return cp === 0x20 || cp === 0x09 || cp === 0x0a || cp === 0x0d;
}

function describe(cp: number): string {
  return `U+${cp.toString(16).toUpperCase().padStart(4, '0')}`;
}

export function lex(text: ScalarText): LexToken[] {
  const tokens: LexToken[] = [];
  const n = text.scalarLength;
  let i = 0;

  const push = (kind: LexKind, value: string, start: number, end: number): void => {
    tokens.push({ kind, value, start, end });
  };

  while (i < n) {
    const cp = text.scalarAt(i);

    if (isWhitespace(cp)) {
      i++;
      continue;
    }

    // Line comment.
    if (cp === 0x2f && text.scalars[i + 1] === 0x2f) {
      while (i < n && text.scalarAt(i) !== 0x0a) i++;
      continue;
    }

    // Block comment.
    if (cp === 0x2f && text.scalars[i + 1] === 0x2a) {
      const start = i;
      i += 2;
      while (i < n && !(text.scalarAt(i) === 0x2a && text.scalars[i + 1] === 0x2f)) i++;
      if (i >= n) {
        throw new SyntaxError(`unterminated block comment at scalar offset ${start}`);
      }
      i += 2;
      continue;
    }

    if (isIdStart(cp)) {
      const start = i;
      i++;
      while (i < n && isIdContinue(text.scalarAt(i))) i++;
      const value = text.sliceScalars(start, i);
      push(KEYWORDS.has(value) ? 'keyword' : 'ident', value, start, i);
      continue;
    }

    if (isDigit(cp)) {
      const start = i;
      i++;
      while (i < n && isDigit(text.scalarAt(i))) i++;
      if (text.scalars[i] === 0x2e && isDigit(text.scalars[i + 1] ?? -1)) {
        i++;
        while (i < n && isDigit(text.scalarAt(i))) i++;
      }
      push('number', text.sliceScalars(start, i), start, i);
      continue;
    }

    if (cp === 0x22) {
      const start = i;
      i++;
      let value = '';
      let closed = false;
      while (i < n) {
        const c = text.scalarAt(i);
        if (c === 0x22) {
          closed = true;
          i++;
          break;
        }
        if (c === 0x5c && i + 1 < n) {
          const esc = text.scalarAt(i + 1);
          if (esc === 0x6e) value += '\n';
          else if (esc === 0x74) value += '\t';
          else if (esc === 0x72) value += '\r';
          else value += String.fromCodePoint(esc);
          i += 2;
          continue;
        }
        value += String.fromCodePoint(c);
        i++;
      }
      if (!closed) {
        throw new SyntaxError(`unterminated string at scalar offset ${start}`);
      }
      push('string', value, start, i);
      continue;
    }

    const ch = String.fromCodePoint(cp);
    if (PUNCTS.has(ch)) {
      push('punct', ch, i, i + 1);
      i++;
      continue;
    }

    throw new SyntaxError(`unexpected character ${describe(cp)} at scalar offset ${i}`);
  }

  push('eof', '', n, n);
  return tokens;
}
