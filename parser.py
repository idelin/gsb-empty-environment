"""Tiny recursive-descent parser (no host eval, no external libraries).

Grammar:
    expr   := term (('+' | '-') term)*
    term   := factor (('*' | '/') factor)*
    factor := '-' factor | atom
    atom   := NUMBER | NAME | '(' expr ')'
            | 'cond' '(' expr ',' expr ',' expr ')'
    NUMBER := int ('/' int)?          -- exact rationals, e.g. 3/4
"""

from __future__ import annotations

import re
from fractions import Fraction

_TOKEN = re.compile(r"\d+/\d+|\d+|[A-Za-z_]\w*|[()+\-*/,]")
_NUMBER = re.compile(r"\d+(/\d+)?")
_NAME = re.compile(r"[A-Za-z_]\w*")


def parse(factory, text: str):
    tokens = _TOKEN.findall(text)
    pos = 0

    def peek():
        return tokens[pos] if pos < len(tokens) else None

    def advance():
        nonlocal pos
        tok = tokens[pos]
        pos += 1
        return tok

    def expect(tok):
        if advance() != tok:
            raise SyntaxError(f"expected {tok!r} in {text!r}")

    def parse_expr():
        node = parse_term()
        while peek() in ("+", "-"):
            op = advance()
            rhs = parse_term()
            node = factory.add(node, rhs) if op == "+" else factory.sub(node, rhs)
        return node

    def parse_term():
        node = parse_factor()
        while peek() in ("*", "/"):
            op = advance()
            rhs = parse_factor()
            node = factory.mul(node, rhs) if op == "*" else factory.div(node, rhs)
        return node

    def parse_factor():
        tok = peek()
        if tok == "-":
            advance()
            return factory.neg(parse_factor())
        if tok == "(":
            advance()
            node = parse_expr()
            expect(")")
            return node
        if tok == "cond":
            advance()
            expect("(")
            c = parse_expr()
            expect(",")
            t = parse_expr()
            expect(",")
            e = parse_expr()
            expect(")")
            return factory.cond(c, t, e)
        if tok is not None and _NUMBER.fullmatch(tok):
            advance()
            num, slash, den = tok.partition("/")
            if slash and int(den) == 0:
                # literal like 1/0 is a division-by-zero *expression*,
                # not a parser error: keep it as a div node (empty domain)
                return factory.div(factory.const(int(num)),
                                   factory.const(int(den)))
            return factory.const(Fraction(tok))
        if tok is not None and _NAME.fullmatch(tok):
            advance()
            return factory.var(tok)
        raise SyntaxError(f"unexpected token {tok!r} in {text!r}")

    node = parse_expr()
    if peek() is not None:
        raise SyntaxError(f"trailing input in {text!r}")
    return node
