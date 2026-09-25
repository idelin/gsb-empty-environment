"""Recursive-descent parser: source text -> AST.

Grammar:
    program    := stmt* EOF
    stmt       := 'let' IDENT '=' expr ';'
                | IDENT '=' expr ';'
                | 'if' expr block ('else' block)?
                | 'while' expr block
                | 'output' expr ';'
    block      := '{' stmt* '}'
    expr       := comparison
    comparison := additive (('=='|'!='|'<'|'<='|'>'|'>=') additive)?
    additive   := unary (('+'|'-') unary)*
    unary      := '-' unary | primary
    primary    := INT | IDENT | 'input' '(' INT ')' | '(' expr ')'
"""

from __future__ import annotations

from . import ast_nodes as ast
from .lexer import Token, tokenize

BINOP_PRECEDENCE = {
    "==": 1, "!=": 1, "<": 1, "<=": 1, ">": 1, ">=": 1,
    "+": 2, "-": 2,
    "*": 3, "/": 3, "%": 3,
}


class ParseError(Exception):
    def __init__(self, message: str, span: tuple[int, int]):
        super().__init__(f"parse error: {message} at byte range [{span[0]}, {span[1]})")
        self.span = span


class Parser:
    def __init__(self, tokens: list[Token]):
        self.tokens = tokens
        self.pos = 0

    # -- token helpers --
    def peek(self) -> Token:
        return self.tokens[self.pos]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def at(self, kind: str, text: str | None = None) -> bool:
        tok = self.peek()
        return tok.kind == kind and (text is None or tok.text == text)

    def expect(self, kind: str, text: str | None = None) -> Token:
        tok = self.peek()
        if tok.kind != kind or (text is not None and tok.text != text):
            want = text if text is not None else kind
            raise ParseError(f"expected {want!r} but found {tok.text!r}", tok.span())
        return self.advance()

    # -- entry point --
    def parse_program(self) -> ast.Program:
        body: list[ast.Stmt] = []
        while not self.at("EOF"):
            body.append(self.parse_stmt())
        eof = self.peek()
        start = body[0].span[0] if body else eof.start
        return ast.Program(body, (start, eof.end))

    def parse_stmt(self) -> ast.Stmt:
        tok = self.peek()
        if tok.kind == "KEYWORD" and tok.text == "let":
            self.advance()
            name = self.expect("IDENT")
            self.expect("PUNCT", "=")
            expr = self.parse_expr()
            semi = self.expect("PUNCT", ";")
            return ast.Let(name.text, expr, (tok.start, semi.end))
        if tok.kind == "KEYWORD" and tok.text == "output":
            self.advance()
            expr = self.parse_expr()
            semi = self.expect("PUNCT", ";")
            return ast.Output(expr, (tok.start, semi.end))
        if tok.kind == "KEYWORD" and tok.text == "if":
            return self.parse_if()
        if tok.kind == "KEYWORD" and tok.text == "while":
            return self.parse_while()
        if tok.kind == "IDENT":
            name = self.advance()
            self.expect("PUNCT", "=")
            expr = self.parse_expr()
            semi = self.expect("PUNCT", ";")
            return ast.Assign(name.text, expr, (name.start, semi.end))
        raise ParseError(f"unexpected token {tok.text!r}", tok.span())

    def parse_if(self) -> ast.If:
        start_tok = self.expect("KEYWORD", "if")
        cond = self.parse_expr()
        then_body = self.parse_block()
        else_body: list[ast.Stmt] = []
        end = then_body[-1].span[1] if then_body else cond.span[1]
        if self.at("KEYWORD", "else"):
            self.advance()
            else_body = self.parse_block()
            end = else_body[-1].span[1] if else_body else end
        return ast.If(cond, then_body, else_body, (start_tok.start, end))

    def parse_while(self) -> ast.While:
        start_tok = self.expect("KEYWORD", "while")
        cond = self.parse_expr()
        body = self.parse_block()
        end = body[-1].span[1] if body else cond.span[1]
        return ast.While(cond, body, (start_tok.start, end))

    def parse_block(self) -> list[ast.Stmt]:
        self.expect("PUNCT", "{")
        body: list[ast.Stmt] = []
        while not self.at("PUNCT", "}"):
            if self.at("EOF"):
                raise ParseError("unterminated block, expected '}'", self.peek().span())
            body.append(self.parse_stmt())
        self.expect("PUNCT", "}")
        return body

    # -- expressions (precedence climbing) --
    def parse_expr(self, min_prec: int = 1) -> ast.Expr:
        left = self.parse_unary()
        while True:
            tok = self.peek()
            if tok.kind != "OP" or tok.text not in BINOP_PRECEDENCE:
                break
            prec = BINOP_PRECEDENCE[tok.text]
            if prec < min_prec:
                break
            self.advance()
            right = self.parse_expr(prec + 1)
            left = ast.BinOp(tok.text, left, right, (left.span[0], right.span[1]))
        return left

    def parse_unary(self) -> ast.Expr:
        tok = self.peek()
        if tok.kind == "OP" and tok.text == "-":
            self.advance()
            operand = self.parse_unary()
            return ast.Neg(operand, (tok.start, operand.span[1]))
        return self.parse_primary()

    def parse_primary(self) -> ast.Expr:
        tok = self.peek()
        if tok.kind == "INT":
            self.advance()
            return ast.IntLit(int(tok.text), tok.span())
        if tok.kind == "IDENT":
            self.advance()
            return ast.Var(tok.text, tok.span())
        if tok.kind == "KEYWORD" and tok.text == "input":
            self.advance()
            self.expect("PUNCT", "(")
            idx = self.expect("INT")
            close = self.expect("PUNCT", ")")
            return ast.Input(int(idx.text), (tok.start, close.end))
        if tok.kind == "PUNCT" and tok.text == "(":
            self.advance()
            expr = self.parse_expr()
            close = self.expect("PUNCT", ")")
            expr.span = (tok.start, close.end)
            return expr
        raise ParseError(f"expected expression but found {tok.text!r}", tok.span())


def parse(source: str) -> ast.Program:
    return Parser(tokenize(source)).parse_program()
