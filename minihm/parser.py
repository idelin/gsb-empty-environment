"""Recursive-descent parser for the mini language.

Grammar (loosely)::

    program := expr EOF
    expr    := "let" IDENT "=" expr "in" expr
             | "if" expr "then" expr "else" expr
             | "fun" IDENT "->" expr
             | compare
    compare := add (("==" | "<") add)*
    add     := mul (("+" | "-") mul)*
    mul     := app ("*" app)*
    app     := atom atom*            -- juxtaposition is application
    atom    := INT | "true" | "false" | IDENT | "(" expr ")"
"""
from __future__ import annotations

from .astnodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Node, Var
from .errors import ParseError
from .lexer import Token, lex
from .source import Source, merge_span


class Parser:
    def __init__(self, source: Source):
        self.src = source
        self.tokens = lex(source)
        self.pos = 0

    # -- token helpers -------------------------------------------------
    def peek(self) -> Token:
        return self.tokens[self.pos]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def at_kw(self, word: str) -> bool:
        tok = self.peek()
        return tok.kind == "KW" and tok.text == word

    def at_op(self, op: str) -> bool:
        tok = self.peek()
        return tok.kind == "OP" and tok.text == op

    def expect_kw(self, word: str) -> Token:
        if not self.at_kw(word):
            raise ParseError(
                f"expected `{word}`, found {self.peek().text!r}", self.peek().span)
        return self.advance()

    def expect_op(self, op: str) -> Token:
        if not self.at_op(op):
            raise ParseError(
                f"expected `{op}`, found {self.peek().text!r}", self.peek().span)
        return self.advance()

    # -- grammar -------------------------------------------------------
    def parse_program(self) -> Node:
        expr = self.parse_expr()
        if self.peek().kind != "EOF":
            raise ParseError(
                f"unexpected {self.peek().text!r} after expression", self.peek().span)
        return expr

    def parse_expr(self) -> Node:
        if self.at_kw("let"):
            return self.parse_let()
        if self.at_kw("if"):
            return self.parse_if()
        if self.at_kw("fun"):
            return self.parse_fun()
        return self.parse_compare()

    def parse_let(self) -> Node:
        kw = self.expect_kw("let")
        name = self.peek()
        if name.kind != "IDENT":
            raise ParseError(
                f"expected a name after `let`, found {name.text!r}", name.span)
        self.advance()
        self.expect_op("=")
        value = self.parse_expr()
        self.expect_kw("in")
        body = self.parse_expr()
        return Let(merge_span(kw.span, body.span), name.text, name.span, value, body)

    def parse_if(self) -> Node:
        kw = self.expect_kw("if")
        cond = self.parse_expr()
        self.expect_kw("then")
        then = self.parse_expr()
        self.expect_kw("else")
        otherwise = self.parse_expr()
        return If(merge_span(kw.span, otherwise.span), cond, then, otherwise)

    def parse_fun(self) -> Node:
        kw = self.expect_kw("fun")
        name = self.peek()
        if name.kind != "IDENT":
            raise ParseError(
                f"expected a parameter name after `fun`, found {name.text!r}",
                name.span)
        self.advance()
        self.expect_op("->")
        body = self.parse_expr()
        return Lam(merge_span(kw.span, body.span), name.text, name.span, body)

    def parse_compare(self) -> Node:
        left = self.parse_add()
        while self.at_op("==") or self.at_op("<"):
            op = self.advance()
            right = self.parse_add()
            left = BinOp(merge_span(left.span, right.span),
                         op.text, op.span, left, right)
        return left

    def parse_add(self) -> Node:
        left = self.parse_mul()
        while self.at_op("+") or self.at_op("-"):
            op = self.advance()
            right = self.parse_mul()
            left = BinOp(merge_span(left.span, right.span),
                         op.text, op.span, left, right)
        return left

    def parse_mul(self) -> Node:
        left = self.parse_app()
        while self.at_op("*"):
            op = self.advance()
            right = self.parse_app()
            left = BinOp(merge_span(left.span, right.span),
                         op.text, op.span, left, right)
        return left

    def parse_app(self) -> Node:
        expr = self.parse_atom()
        while self._starts_atom():
            arg = self.parse_atom()
            expr = App(merge_span(expr.span, arg.span), expr, arg)
        return expr

    def _starts_atom(self) -> bool:
        tok = self.peek()
        if tok.kind in ("INT", "IDENT", "LP"):
            return True
        return tok.kind == "KW" and tok.text in ("true", "false")

    def parse_atom(self) -> Node:
        tok = self.peek()
        if tok.kind == "INT":
            self.advance()
            return IntLit(tok.span, int(tok.text))
        if tok.kind == "IDENT":
            self.advance()
            return Var(tok.span, tok.text)
        if tok.kind == "KW" and tok.text in ("true", "false"):
            self.advance()
            return BoolLit(tok.span, tok.text == "true")
        if tok.kind == "LP":
            self.advance()
            expr = self.parse_expr()
            if self.peek().kind != "RP":
                raise ParseError("expected `)`", self.peek().span)
            self.advance()
            return expr
        raise ParseError(f"unexpected {tok.text!r}", tok.span)
