"""Recursive-descent parser.

Grammar (whitespace-insensitive, '#' comments):

    program  := expr EOF
    expr     := "let" binding ("," binding)* "in" expr | arith
    binding  := IDENT "=" expr
    arith    := term (("+" | "-") term)*
    term     := factor (("*" | "/") factor)*
    factor   := "-" factor | atom
    atom     := INT | IDENT | "(" expr ")"
"""

from __future__ import annotations

from .ast_nodes import BinOp, Binding, IntLit, Let, Neg, Node, Var
from .errors import ParseError
from .lexer import Token, tokenize

BINOPS = {"PLUS": "+", "MINUS": "-", "STAR": "*", "SLASH": "/"}


class Parser:
    def __init__(self, tokens: list[Token]) -> None:
        self.tokens = tokens
        self.pos = 0

    @property
    def current(self) -> Token:
        return self.tokens[self.pos]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def expect(self, kind: str) -> Token:
        tok = self.current
        if tok.kind != kind:
            raise ParseError(f"expected {kind}, got {tok.kind} {tok.text!r}", tok.span)
        return self.advance()

    def parse_program(self) -> Node:
        node = self.parse_expr()
        self.expect("EOF")
        return node

    def parse_expr(self) -> Node:
        if self.current.kind == "LET":
            return self.parse_let()
        return self.parse_arith()

    def parse_let(self) -> Let:
        let_tok = self.expect("LET")
        bindings = [self.parse_binding()]
        while self.current.kind == "COMMA":
            self.advance()
            bindings.append(self.parse_binding())
        self.expect("IN")
        body = self.parse_expr()
        return Let((let_tok.span[0], body.span[1]), tuple(bindings), body)

    def parse_binding(self) -> Binding:
        name_tok = self.expect("IDENT")
        self.expect("EQUALS")
        expr = self.parse_expr()
        return Binding(name_tok.text, name_tok.span, expr)

    def parse_arith(self) -> Node:
        node = self.parse_term()
        while self.current.kind in ("PLUS", "MINUS"):
            op = BINOPS[self.advance().kind]
            right = self.parse_term()
            node = BinOp((node.span[0], right.span[1]), op, node, right)
        return node

    def parse_term(self) -> Node:
        node = self.parse_factor()
        while self.current.kind in ("STAR", "SLASH"):
            op = BINOPS[self.advance().kind]
            right = self.parse_factor()
            node = BinOp((node.span[0], right.span[1]), op, node, right)
        return node

    def parse_factor(self) -> Node:
        if self.current.kind == "MINUS":
            tok = self.advance()
            operand = self.parse_factor()
            return Neg((tok.span[0], operand.span[1]), operand)
        return self.parse_atom()

    def parse_atom(self) -> Node:
        tok = self.current
        if tok.kind == "INT":
            self.advance()
            return IntLit(tok.span, int(tok.text))
        if tok.kind == "IDENT":
            self.advance()
            return Var(tok.span, tok.text)
        if tok.kind == "LPAREN":
            self.advance()
            node = self.parse_expr()
            self.expect("RPAREN")
            return node
        raise ParseError(f"unexpected {tok.kind} {tok.text!r}", tok.span)


def parse(src: str) -> Node:
    return Parser(tokenize(src)).parse_program()
