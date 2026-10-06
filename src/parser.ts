import type { Expr } from "./ast.ts";
import { lex, type Token } from "./lexer.ts";

// Grammar:
//   expr     := term ("+" term)*
//   term     := factor (("*" | "/") factor)*
//   factor   := primary ("^" exponent)*
//   exponent := "-"? INTEGER | "(" "-"? INTEGER ")"
//   primary  := NUMBER | IDENT | "(" expr ")"
export function parseFormula(src: string): Expr {
  const tokens = lex(src);
  let pos = 0;

  const peek = (): Token => tokens[pos];

  const isOp = (op: string): boolean =>
    peek().type === "op" && (peek() as { op: string }).op === op;

  const expectOp = (op: string): void => {
    if (!isOp(op)) throw new Error(`expected '${op}' at token ${pos}`);
    pos++;
  };

  function parseExpr(): Expr {
    let left = parseTerm();
    while (isOp("+")) {
      pos++;
      left = { kind: "add", left, right: parseTerm() };
    }
    return left;
  }

  function parseTerm(): Expr {
    let left = parseFactor();
    while (isOp("*") || isOp("/")) {
      const op = (peek() as { op: string }).op;
      pos++;
      left = { kind: op === "*" ? "mul" : "div", left, right: parseFactor() };
    }
    return left;
  }

  function parseFactor(): Expr {
    let base = parsePrimary();
    while (isOp("^")) {
      pos++;
      base = { kind: "pow", base, exponent: parseExponent() };
    }
    return base;
  }

  function parseExponent(): number {
    let parenthesized = false;
    let sign = 1;
    if (isOp("(")) {
      pos++;
      parenthesized = true;
    }
    if (isOp("-")) {
      sign = -1;
      pos++;
    }
    const token = peek();
    if (token.type !== "num" || !Number.isInteger(token.value)) {
      throw new Error("exponent must be an integer literal");
    }
    pos++;
    if (parenthesized) expectOp(")");
    return sign * token.value;
  }

  function parsePrimary(): Expr {
    const token = peek();
    if (token.type === "num") {
      pos++;
      return { kind: "num", value: token.value, raw: token.raw };
    }
    if (token.type === "ident") {
      pos++;
      return { kind: "var", name: token.name };
    }
    if (isOp("(")) {
      pos++;
      const inner = parseExpr();
      expectOp(")");
      return inner;
    }
    if (isOp("-")) {
      throw new Error("unary minus is only supported in exponents, e.g. x^-2");
    }
    throw new Error(`unexpected token at position ${pos}`);
  }

  const expr = parseExpr();
  if (peek().type !== "eof") {
    throw new Error("unexpected trailing input");
  }
  return expr;
}
