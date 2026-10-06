export type Token =
  | { type: "num"; raw: string; value: number }
  | { type: "ident"; name: string }
  | { type: "op"; op: "+" | "-" | "*" | "/" | "^" | "(" | ")" }
  | { type: "eof" };

const NUMBER_RE = /^(?:\d+(?:\.\d+)?|\.\d+)(?:[eE][+-]?\d+)?/;
const IDENT_RE = /^[A-Za-z_][A-Za-z0-9_]*/;

export function lex(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (/\s/.test(ch)) {
      i++;
      continue;
    }
    if (/[0-9.]/.test(ch)) {
      const match = NUMBER_RE.exec(src.slice(i));
      if (!match) throw new Error(`invalid number at offset ${i}`);
      tokens.push({ type: "num", raw: match[0], value: Number(match[0]) });
      i += match[0].length;
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      const match = IDENT_RE.exec(src.slice(i))!;
      tokens.push({ type: "ident", name: match[0] });
      i += match[0].length;
      continue;
    }
    if ("+-*/^()".includes(ch)) {
      tokens.push({ type: "op", op: ch as "+" | "-" | "*" | "/" | "^" | "(" | ")" });
      i++;
      continue;
    }
    throw new Error(`unexpected character '${ch}' at offset ${i}`);
  }
  tokens.push({ type: "eof" });
  return tokens;
}
