export type TokenKind = 'ident' | 'number' | 'string' | 'punct' | 'eof';

export interface Token {
  kind: TokenKind;
  value: string;
  start: number;
  end: number;
}

export const KEYWORDS = new Set(['let', 'fn', 'return', 'import', 'from', 'as']);

const PUNCTS = new Set(['{', '}', '(', ')', ';', ',', '=', '+']);

function isIdentStart(ch: string): boolean {
  return /[A-Za-z_$]/.test(ch);
}

function isIdentPart(ch: string): boolean {
  return /[A-Za-z0-9_$]/.test(ch);
}

export function tokenize(src: string, file: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n') {
      i++;
      continue;
    }
    if (ch === '/' && src[i + 1] === '/') {
      while (i < src.length && src[i] !== '\n') i++;
      continue;
    }
    if (isIdentStart(ch)) {
      const start = i;
      while (i < src.length && isIdentPart(src[i])) i++;
      tokens.push({ kind: 'ident', value: src.slice(start, i), start, end: i });
      continue;
    }
    if (/[0-9]/.test(ch)) {
      const start = i;
      while (i < src.length && /[0-9]/.test(src[i])) i++;
      tokens.push({ kind: 'number', value: src.slice(start, i), start, end: i });
      continue;
    }
    if (ch === '"') {
      const start = i;
      i++;
      while (i < src.length && src[i] !== '"') i++;
      if (i >= src.length) throw new Error(`${file}: unterminated string at ${start}`);
      i++;
      tokens.push({ kind: 'string', value: src.slice(start + 1, i - 1), start, end: i });
      continue;
    }
    if (PUNCTS.has(ch)) {
      tokens.push({ kind: 'punct', value: ch, start: i, end: i + 1 });
      i++;
      continue;
    }
    throw new Error(`${file}: unexpected character ${JSON.stringify(ch)} at ${i}`);
  }
  tokens.push({ kind: 'eof', value: '', start: src.length, end: src.length });
  return tokens;
}
