/**
 * Independent recursive-descent recognizer for the expression grammar.
 * Used as an oracle to cross-check the PDA-based repairer.
 */

import type { TokenKind } from './tokens.ts';

const FIRST_FACTOR: TokenKind[] = ['int', 'ident', '(', '['];

export function parses(kinds: TokenKind[]): boolean {
  let pos = 0;
  const peek = (): TokenKind | null => (pos < kinds.length ? kinds[pos] : null);

  function parseExpr(): boolean {
    if (!parseTerm()) return false;
    while (peek() === '+' || peek() === '-') {
      pos++;
      if (!parseTerm()) return false;
    }
    return true;
  }

  function parseTerm(): boolean {
    if (!parseFactor()) return false;
    while (peek() === '*' || peek() === '/') {
      pos++;
      if (!parseFactor()) return false;
    }
    return true;
  }

  function parseFactor(): boolean {
    const t = peek();
    if (t === 'int') { pos++; return true; }
    if (t === 'ident') {
      pos++;
      if (peek() !== '(') return false;
      pos++;
      if (!parseList()) return false;
      if (peek() !== ')') return false;
      pos++;
      return true;
    }
    if (t === '(') {
      pos++;
      if (!parseExpr()) return false;
      if (peek() !== ')') return false;
      pos++;
      return true;
    }
    if (t === '[') {
      pos++;
      if (!parseList()) return false;
      if (peek() !== ']') return false;
      pos++;
      return true;
    }
    return false;
  }

  /** args / elems: (expr (',' expr)*)? */
  function parseList(): boolean {
    const t = peek();
    if (t !== null && FIRST_FACTOR.includes(t)) {
      if (!parseExpr()) return false;
      while (peek() === ',') {
        pos++;
        if (!parseExpr()) return false;
      }
    }
    return true;
  }

  if (!parseExpr()) return false;
  return pos === kinds.length;
}
