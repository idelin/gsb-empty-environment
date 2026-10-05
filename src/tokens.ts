/** Token definitions and lexer. Original tokens keep their char offset in `pos`. */

export type TokenKind =
  | 'int' | 'ident'
  | '+' | '-' | '*' | '/'
  | '(' | ')' | '[' | ']' | ',';

export interface Token {
  kind: TokenKind;
  text: string;
  /** Char offset in the source string; `null` for tokens inserted by a repair. */
  pos: number | null;
}

export const PUNCT: TokenKind[] = ['+', '-', '*', '/', '(', ')', '[', ']', ','];

/** Every terminal the parser can expect, i.e. every insertable token kind. */
export const TERMINALS: TokenKind[] = ['int', 'ident', ...PUNCT];

/** Canonical spelling of an inserted token (ints become `0`, idents become `a`). */
export function canonicalText(kind: TokenKind): string {
  if (kind === 'int') return '0';
  if (kind === 'ident') return 'a';
  return kind;
}

export function lex(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (/\s/.test(ch)) { i++; continue; }
    if (/[0-9]/.test(ch)) {
      let j = i;
      while (j < src.length && /[0-9]/.test(src[j])) j++;
      tokens.push({ kind: 'int', text: src.slice(i, j), pos: i });
      i = j;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      let j = i;
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++;
      tokens.push({ kind: 'ident', text: src.slice(i, j), pos: i });
      i = j;
      continue;
    }
    if ((PUNCT as string[]).includes(ch)) {
      tokens.push({ kind: ch as TokenKind, text: ch, pos: i });
      i++;
      continue;
    }
    throw new Error(`lexer: unexpected character ${JSON.stringify(ch)} at offset ${i}`);
  }
  return tokens;
}
