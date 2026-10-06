import type { Expr } from "./ast.ts";

/**
 * Grammar (precedence low -> high):
 *   expr := term (('+' | '-') term)*
 *   term := power (('*' | '/') power)*
 *   power := atom ('^' signed-integer)?
 *   atom := number | ident | '(' expr ')'
 */
export function parse(source: string): Expr {
  let pos = 0;

  const peek = (): string => source[pos] ?? "";
  const skipWs = (): void => {
    while (pos < source.length && /\s/.test(source[pos]!)) pos++;
  };

  const fail = (msg: string): never => {
    throw new SyntaxError(`parse error at ${pos}: ${msg}`);
  };

  const parseAtom = (): Expr => {
    skipWs();
    const start = pos;
    const ch = peek();
    if (ch === "(") {
      pos++;
      const inner = parseExpr();
      skipWs();
      if (peek() !== ")") fail("expected ')'");
      pos++;
      // Parentheses keep the inner node; its span already excludes parens,
      // so widen nothing here — spans of inner cover its own text.
      return inner;
    }
    if (/[0-9.]/.test(ch)) {
      const m = /^[0-9]+(?:\.[0-9]+)?/.exec(source.slice(pos));
      if (!m) fail("invalid number");
      pos += m[0].length;
      return { kind: "num", value: Number(m[0]), start, end: pos };
    }
    if (/[A-Za-z_]/.test(ch)) {
      const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(source.slice(pos))!;
      pos += m[0].length;
      return { kind: "var", name: m[0], start, end: pos };
    }
    return fail(`unexpected character '${ch}'`);
  };

  const parsePower = (): Expr => {
    const base = parseAtom();
    skipWs();
    if (peek() === "^") {
      pos++;
      skipWs();
      const expStart = pos;
      if (peek() === "-" || peek() === "+") pos++;
      const m = /^[0-9]+/.exec(source.slice(pos));
      if (!m) fail("expected integer exponent after '^'");
      pos += m[0].length;
      const exponent = Number(source.slice(expStart, pos));
      return { kind: "pow", base, exponent, start: base.start, end: pos };
    }
    return base;
  };

  const parseTerm = (): Expr => {
    let left = parsePower();
    for (;;) {
      skipWs();
      const ch = peek();
      if (ch !== "*" && ch !== "/") return left;
      pos++;
      const right = parsePower();
      left = { kind: "mul", op: ch, left, right, start: left.start, end: right.end };
    }
  };

  const parseExpr = (): Expr => {
    let left = parseTerm();
    for (;;) {
      skipWs();
      const ch = peek();
      if (ch !== "+" && ch !== "-") return left;
      pos++;
      const right = parseTerm();
      left = { kind: "add", op: ch, left, right, start: left.start, end: right.end };
    }
  };

  const result = parseExpr();
  skipWs();
  if (pos < source.length) fail("unexpected trailing input");
  return result;
}
