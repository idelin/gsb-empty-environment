/** Recursive-descent parser with precedence climbing. */

import type { BinOp, Expr, UnOp } from "./ast.ts";
import { tokenize, type Token } from "./lexer.ts";

export const BIN_PREC: Record<BinOp, number> = {
  "||": 1,
  "&&": 2,
  "==": 3, "!=": 3,
  "<": 4, "<=": 4, ">": 4, ">=": 4,
  "+": 5, "-": 5,
  "*": 6, "/": 6, "%": 6,
};

export const UNARY_PREC = 7;

export function parse(src: string): Expr {
  const tokens = tokenize(src);
  let pos = 0;

  const peek = (): Token => tokens[pos];
  const advance = (): Token => tokens[pos++];

  const isPunct = (t: Token, value: string): boolean =>
    t.type === "punct" && t.value === value;

  const expectPunct = (value: string): void => {
    const t = advance();
    if (!isPunct(t, value)) {
      throw new SyntaxError(`Expected '${value}' but found ${JSON.stringify(t)}`);
    }
  };

  function parseExpr(minPrec: number): Expr {
    let left = parseUnary();
    for (;;) {
      const t = peek();
      if (t.type !== "punct") break;
      const prec = (BIN_PREC as Record<string, number | undefined>)[t.value];
      if (prec === undefined || prec < minPrec) break;
      advance();
      const right = parseExpr(prec + 1);
      left = { kind: "binary", op: t.value as BinOp, left, right };
    }
    return left;
  }

  function parseUnary(): Expr {
    const t = peek();
    if (isPunct(t, "-") || isPunct(t, "!")) {
      advance();
      const operand = parseUnary();
      return { kind: "unary", op: t.value as UnOp, operand };
    }
    return parsePostfix();
  }

  function parsePostfix(): Expr {
    let expr = parseAtom();
    for (;;) {
      if (!isPunct(peek(), "(")) break;
      advance();
      const args: Expr[] = [];
      if (!isPunct(peek(), ")")) {
        args.push(parseExpr(0));
        while (isPunct(peek(), ",")) {
          advance();
          args.push(parseExpr(0));
        }
      }
      expectPunct(")");
      expr = { kind: "call", callee: expr, args };
    }
    return expr;
  }

  function parseAtom(): Expr {
    const t = advance();
    switch (t.type) {
      case "num":
        return { kind: "num", value: t.value };
      case "str":
        return { kind: "str", value: t.value };
      case "ident":
        if (t.name === "true") return { kind: "bool", value: true };
        if (t.name === "false") return { kind: "bool", value: false };
        return { kind: "ident", name: t.name };
      case "punct":
        if (t.value === "(") {
          const inner = parseExpr(0);
          expectPunct(")");
          return inner;
        }
        break;
    }
    throw new SyntaxError(`Unexpected token ${JSON.stringify(t)}`);
  }

  const expr = parseExpr(0);
  if (peek().type !== "eof") {
    throw new SyntaxError(`Unexpected trailing token ${JSON.stringify(peek())}`);
  }
  return expr;
}
