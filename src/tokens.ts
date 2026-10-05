/** Token definitions and tokenizer for the expression language. */

export type TokenType =
  | "INT" | "IDENT"
  | "LPAREN" | "RPAREN" | "LBRACKET" | "RBRACKET" | "COMMA"
  | "PLUS" | "MINUS" | "STAR" | "SLASH"
  | "INVALID" // any character that is not part of the language
  | "EOF";    // virtual end-of-input marker, never produced by tokenize()

export interface Token {
  type: TokenType;
  text: string;
  /** 0-based offset of the token in the original source. */
  pos: number;
}

const PUNCT: Record<string, TokenType> = {
  "(": "LPAREN",
  ")": "RPAREN",
  "[": "LBRACKET",
  "]": "RBRACKET",
  ",": "COMMA",
  "+": "PLUS",
  "-": "MINUS",
  "*": "STAR",
  "/": "SLASH",
};

/** Terminals that may be inserted by the repairer (never EOF / INVALID). */
export const INSERTABLE: readonly TokenType[] = [
  "INT", "IDENT",
  "LPAREN", "RPAREN", "LBRACKET", "RBRACKET", "COMMA",
  "PLUS", "MINUS", "STAR", "SLASH",
];

/** Canonical source text used for an inserted token of each type. */
export const INSERT_TEXT: Record<TokenType, string> = {
  INT: "0",
  IDENT: "x",
  LPAREN: "(",
  RPAREN: ")",
  LBRACKET: "[",
  RBRACKET: "]",
  COMMA: ",",
  PLUS: "+",
  MINUS: "-",
  STAR: "*",
  SLASH: "/",
  INVALID: "?",
  EOF: "<eof>",
};

export function tokenize(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (/\s/.test(ch)) {
      i++;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      let j = i;
      while (j < src.length && /[0-9]/.test(src[j])) j++;
      tokens.push({ type: "INT", text: src.slice(i, j), pos: i });
      i = j;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      let j = i;
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++;
      tokens.push({ type: "IDENT", text: src.slice(i, j), pos: i });
      i = j;
      continue;
    }
    const punct = PUNCT[ch];
    if (punct !== undefined) {
      tokens.push({ type: punct, text: ch, pos: i });
      i++;
      continue;
    }
    // Lexical errors become INVALID tokens; the repairer can delete them.
    tokens.push({ type: "INVALID", text: ch, pos: i });
    i++;
  }
  return tokens;
}
