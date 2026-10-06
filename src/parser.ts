import { lex, type Token } from "./lexer.ts";
import type { Block, Expr, LetDecl, Program, Stmt } from "./ast.ts";

export class ParseError extends Error {
  offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = "ParseError";
    this.offset = offset;
  }
}

export function parse(source: string): Program {
  const tokens = lex(source);
  let pos = 0;

  const peek = (): Token => tokens[pos];
  const advance = (): Token => tokens[pos++];

  function expectPunct(value: string): Token {
    const t = advance();
    if (t.type !== "punct" || t.value !== value) {
      throw new ParseError(
        `expected '${value}' but found ${t.type === "eof" ? "end of input" : `'${t.value}'`}`,
        t.span.start,
      );
    }
    return t;
  }

  function parseBlock(): Block {
    const open = expectPunct("{");
    const body: Stmt[] = [];
    while (!(peek().type === "punct" && peek().value === "}")) {
      if (peek().type === "eof") throw new ParseError("unterminated block", peek().span.start);
      body.push(parseStmt());
    }
    const close = expectPunct("}");
    return { kind: "Block", body, span: { start: open.span.start, end: close.span.end } };
  }

  function parseLet(): LetDecl {
    const kw = advance();
    const name = advance();
    if (name.type !== "ident") {
      throw new ParseError("expected identifier after 'let'", name.span.start);
    }
    expectPunct("=");
    const init = parseExpr();
    const semi = expectPunct(";");
    return {
      kind: "LetDecl",
      name: name.value,
      nameSpan: name.span,
      init,
      span: { start: kw.span.start, end: semi.span.end },
    };
  }

  function parseExprStmt(): Stmt {
    const expr = parseExpr();
    const semi = expectPunct(";");
    return { kind: "ExprStmt", expr, span: { start: expr.span.start, end: semi.span.end } };
  }

  function parseStmt(): Stmt {
    const t = peek();
    if (t.type === "keyword" && t.value === "let") return parseLet();
    if (t.type === "punct" && t.value === "{") return parseBlock();
    return parseExprStmt();
  }

  function parseExpr(): Expr {
    let left = parseTerm();
    while (peek().type === "punct" && (peek().value === "+" || peek().value === "-")) {
      const op = advance().value;
      const right = parseTerm();
      left = { kind: "Binary", op, left, right, span: { start: left.span.start, end: right.span.end } };
    }
    return left;
  }

  function parseTerm(): Expr {
    let left = parseFactor();
    while (peek().type === "punct" && (peek().value === "*" || peek().value === "/")) {
      const op = advance().value;
      const right = parseFactor();
      left = { kind: "Binary", op, left, right, span: { start: left.span.start, end: right.span.end } };
    }
    return left;
  }

  function parseFactor(): Expr {
    const t = advance();
    if (t.type === "ident") return { kind: "Ident", text: t.value, span: t.span };
    if (t.type === "number") return { kind: "NumLit", value: Number(t.value), span: t.span };
    if (t.type === "punct" && t.value === "(") {
      const inner = parseExpr();
      expectPunct(")");
      return inner;
    }
    throw new ParseError(
      `expected expression but found ${t.type === "eof" ? "end of input" : `'${t.value}'`}`,
      t.span.start,
    );
  }

  const body: Stmt[] = [];
  while (peek().type !== "eof") body.push(parseStmt());
  return { kind: "Program", body, span: { start: 0, end: source.length } };
}
