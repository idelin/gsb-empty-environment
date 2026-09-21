"""Recursive-descent parser (no eval, no external parser)."""
from __future__ import annotations

from .ast_nodes import (
    Assign,
    BinOp,
    Expr,
    Halt,
    If,
    IntLit,
    Let,
    Print,
    Stmt,
    Unary,
    Var,
    While,
)
from .lexer import Token, lex

_CMP = {"==", "!=", "<", "<=", ">", ">="}
_ADD = {"+", "-"}
_MUL = {"*", "/", "%"}


class ParseError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.start = start
        self.end = end


def parse(source: str) -> list[Stmt]:
    return _Parser(lex(source)).parse_program()


class _Parser:
    def __init__(self, tokens: list[Token]):
        self.tokens = tokens
        self.pos = 0

    def peek(self, ahead: int = 0) -> Token:
        return self.tokens[min(self.pos + ahead, len(self.tokens) - 1)]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        if tok.kind != "eof":
            self.pos += 1
        return tok

    def expect_text(self, text: str) -> Token:
        tok = self.peek()
        if tok.text != text:
            raise ParseError(
                f"expected {text!r}, found {tok.text!r}", tok.start, tok.end
            )
        return self.advance()

    def expect_ident(self) -> Token:
        tok = self.peek()
        if tok.kind != "ident":
            raise ParseError(
                f"expected identifier, found {tok.text!r}", tok.start, tok.end
            )
        return self.advance()

    def parse_program(self) -> list[Stmt]:
        stmts: list[Stmt] = []
        while self.peek().kind != "eof":
            stmts.append(self.parse_stmt())
        return stmts

    def parse_block(self) -> tuple[list[Stmt], Token]:
        self.expect_text("{")
        stmts: list[Stmt] = []
        while self.peek().text != "}":
            if self.peek().kind == "eof":
                tok = self.peek()
                raise ParseError("unterminated block", tok.start, tok.end)
            stmts.append(self.parse_stmt())
        return stmts, self.advance()

    def parse_stmt(self) -> Stmt:
        tok = self.peek()
        if tok.kind == "keyword":
            if tok.text == "let":
                self.advance()
                name = self.expect_ident()
                self.expect_text("=")
                value = self.parse_expr()
                semi = self.expect_text(";")
                return Let(tok.start, semi.end, name.text, value)
            if tok.text == "print":
                self.advance()
                value = self.parse_expr()
                semi = self.expect_text(";")
                return Print(tok.start, semi.end, value)
            if tok.text == "halt":
                self.advance()
                semi = self.expect_text(";")
                return Halt(tok.start, semi.end)
            if tok.text == "if":
                self.advance()
                cond = self.parse_expr()
                then_body, then_close = self.parse_block()
                else_body: list[Stmt] = []
                end = then_close.end
                if self.peek().text == "else":
                    self.advance()
                    else_body, else_close = self.parse_block()
                    end = else_close.end
                return If(tok.start, end, cond, then_body, else_body)
            if tok.text == "while":
                self.advance()
                cond = self.parse_expr()
                body, close = self.parse_block()
                return While(tok.start, close.end, cond, body)
            raise ParseError(f"unexpected keyword {tok.text!r}", tok.start, tok.end)
        if tok.kind == "ident" and self.peek(1).text == "=":
            name = self.advance()
            self.expect_text("=")
            value = self.parse_expr()
            semi = self.expect_text(";")
            return Assign(tok.start, semi.end, name.text, value)
        raise ParseError(f"unexpected token {tok.text!r}", tok.start, tok.end)

    def parse_expr(self) -> Expr:
        return self.parse_cmp()

    def parse_cmp(self) -> Expr:
        left = self.parse_add()
        while self.peek().kind == "punct" and self.peek().text in _CMP:
            op = self.advance()
            right = self.parse_add()
            left = BinOp(left.start, right.end, op.text, left, right)
        return left

    def parse_add(self) -> Expr:
        left = self.parse_mul()
        while self.peek().kind == "punct" and self.peek().text in _ADD:
            op = self.advance()
            right = self.parse_mul()
            left = BinOp(left.start, right.end, op.text, left, right)
        return left

    def parse_mul(self) -> Expr:
        left = self.parse_unary()
        while self.peek().kind == "punct" and self.peek().text in _MUL:
            op = self.advance()
            right = self.parse_unary()
            left = BinOp(left.start, right.end, op.text, left, right)
        return left

    def parse_unary(self) -> Expr:
        tok = self.peek()
        if tok.kind == "punct" and tok.text in ("-", "!"):
            self.advance()
            operand = self.parse_unary()
            return Unary(tok.start, operand.end, tok.text, operand)
        return self.parse_primary()

    def parse_primary(self) -> Expr:
        tok = self.peek()
        if tok.kind == "int":
            self.advance()
            return IntLit(tok.start, tok.end, int(tok.text))
        if tok.kind == "ident":
            self.advance()
            return Var(tok.start, tok.end, tok.text)
        if tok.text == "(":
            self.advance()
            inner = self.parse_expr()
            self.expect_text(")")
            return inner
        raise ParseError(f"expected expression, found {tok.text!r}", tok.start, tok.end)
