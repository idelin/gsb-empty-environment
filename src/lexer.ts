/** Tokenizer for the expression language. */

export type Token =
  | { type: "num"; value: number }
  | { type: "str"; value: string }
  | { type: "ident"; name: string }
  | { type: "punct"; value: string }
  | { type: "eof" };

const TWO_CHAR_PUNCTS = ["||", "&&", "==", "!=", "<=", ">="];
const ONE_CHAR_PUNCTS = ["<", ">", "+", "-", "*", "/", "%", "!", "(", ")", ","];

const NUMBER_RE = /^\d+(\.\d+)?([eE][+-]?\d+)?/;
const IDENT_START_RE = /[A-Za-z_$]/;
const IDENT_PART_RE = /[A-Za-z0-9_$]/;

export function tokenize(src: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  while (i < src.length) {
    const ch = src[i];

    if (ch === " " || ch === "\t" || ch === "\n" || ch === "\r") {
      i++;
      continue;
    }

    if (ch >= "0" && ch <= "9") {
      const match = NUMBER_RE.exec(src.slice(i));
      if (!match) throw new SyntaxError(`Invalid number at offset ${i}`);
      tokens.push({ type: "num", value: Number(match[0]) });
      i += match[0].length;
      continue;
    }

    if (ch === '"') {
      let j = i + 1;
      for (;;) {
        if (j >= src.length) throw new SyntaxError("Unterminated string literal");
        if (src[j] === "\\") {
          j += 2;
          continue;
        }
        if (src[j] === '"') break;
        j++;
      }
      const raw = src.slice(i, j + 1);
      tokens.push({ type: "str", value: JSON.parse(raw) as string });
      i = j + 1;
      continue;
    }

    if (IDENT_START_RE.test(ch)) {
      let j = i + 1;
      while (j < src.length && IDENT_PART_RE.test(src[j])) j++;
      tokens.push({ type: "ident", name: src.slice(i, j) });
      i = j;
      continue;
    }

    const two = src.slice(i, i + 2);
    if (TWO_CHAR_PUNCTS.includes(two)) {
      tokens.push({ type: "punct", value: two });
      i += 2;
      continue;
    }
    if (ONE_CHAR_PUNCTS.includes(ch)) {
      tokens.push({ type: "punct", value: ch });
      i += 1;
      continue;
    }

    throw new SyntaxError(`Unexpected character '${ch}' at offset ${i}`);
  }

  tokens.push({ type: "eof" });
  return tokens;
}
