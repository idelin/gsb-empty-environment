import type { Span } from "./ast.ts";

export class LexError extends Error {
  offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = "LexError";
    this.offset = offset;
  }
}

export type TokenType = "ident" | "keyword" | "number" | "punct" | "eof";

export interface Token {
  type: TokenType;
  value: string;
  span: Span;
}

export const KEYWORDS: ReadonlySet<string> = new Set(["let"]);

const ID_START = /[A-Za-z_]/;
const ID_PART = /[A-Za-z0-9_]/;
const DIGIT = /[0-9]/;
const PUNCTS = new Set(["{", "}", "(", ")", "=", ";", "+", "-", "*", "/"]);

export function lex(source: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < source.length) {
    const c = source[i];
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      i++;
      continue;
    }
    if (c === "/" && source[i + 1] === "/") {
      while (i < source.length && source[i] !== "\n") i++;
      continue;
    }
    if (ID_START.test(c)) {
      const start = i;
      while (i < source.length && ID_PART.test(source[i])) i++;
      const value = source.slice(start, i);
      tokens.push({
        type: KEYWORDS.has(value) ? "keyword" : "ident",
        value,
        span: { start, end: i },
      });
      continue;
    }
    if (DIGIT.test(c)) {
      const start = i;
      while (i < source.length && DIGIT.test(source[i])) i++;
      tokens.push({ type: "number", value: source.slice(start, i), span: { start, end: i } });
      continue;
    }
    if (PUNCTS.has(c)) {
      tokens.push({ type: "punct", value: c, span: { start: i, end: i + 1 } });
      i++;
      continue;
    }
    throw new LexError(`unexpected character '${c}'`, i);
  }
  tokens.push({ type: "eof", value: "", span: { start: source.length, end: source.length } });
  return tokens;
}
