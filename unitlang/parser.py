"""Hand-written recursive-descent parser producing an AST with byte spans.

Grammar:
    program     := NEWLINE* (stmt (NEWLINE+ stmt)* NEWLINE*)? EOF
    stmt        := IDENT "=" expr | expr
    expr        := comparison
    comparison  := conv (("=="|"!="|"<"|"<="|">"|">=") conv)?
    conv        := additive ("to" unit_expr)?
    additive    := multiplicative (("+"|"-") multiplicative)*
    multiplicative := unary (("*"|"/") unary)*
    unary       := "-" unary | "+" unary | postfix
    postfix     := primary UNIT?            (unit only after a number literal)
    primary     := NUMBER | IDENT | "(" expr ")"
    unit_expr   := IDENT (("*"|"/") IDENT)*
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .errors import ParseError
from .lexer import Token
from .units import UNITS, Unit


@dataclass
class Number:
    value: Fraction
    unit: Unit | None
    span: tuple[int, int]


@dataclass
class Var:
    name: str
    span: tuple[int, int]


@dataclass
class Unary:
    op: str
    operand: object
    op_span: tuple[int, int]
    span: tuple[int, int]


@dataclass
class Binary:
    op: str
    left: object
    right: object
    op_span: tuple[int, int]
    span: tuple[int, int]


@dataclass
class Compare:
    op: str
    left: object
    right: object
    op_span: tuple[int, int]
    span: tuple[int, int]


@dataclass
class Convert:
    expr: object
    unit: Unit
    unit_span: tuple[int, int]
    span: tuple[int, int]


@dataclass
class Assign:
    name: str
    name_span: tuple[int, int]
    expr: object
    span: tuple[int, int]


@dataclass
class ExprStmt:
    expr: object
    span: tuple[int, int]


_COMPARE_OPS = ("==", "!=", "<", "<=", ">", ">=")


def parse_program(tokens: list[Token]) -> list:
    parser = _Parser(tokens)
    stmts = []
    parser._skip_newlines()
    while not parser._at("EOF"):
        stmts.append(parser._stmt())
        if parser._at("EOF"):
            break
        parser._expect("NEWLINE")
        parser._skip_newlines()
    return stmts


class _Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.pos = 0

    def _cur(self) -> Token:
        return self.toks[self.pos]

    def _peek(self, k: int = 1) -> Token:
        return self.toks[min(self.pos + k, len(self.toks) - 1)]

    def _advance(self) -> Token:
        tok = self.toks[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def _at(self, kind: str, text: str | None = None) -> bool:
        tok = self._cur()
        return tok.kind == kind and (text is None or tok.text == text)

    def _at_op(self, *ops: str) -> bool:
        tok = self._cur()
        return tok.kind == "OP" and tok.text in ops

    def _expect(self, kind: str) -> Token:
        tok = self._cur()
        if tok.kind != kind:
            raise ParseError(f"expected {kind.lower()}, got {tok.text!r}", tok.span)
        return self._advance()

    def _skip_newlines(self):
        while self._at("NEWLINE"):
            self._advance()

    def _stmt(self):
        tok = self._cur()
        nxt = self._peek()
        if (
            tok.kind == "IDENT"
            and tok.text != "to"
            and nxt.kind == "OP"
            and nxt.text == "="
        ):
            self._advance()
            self._advance()
            expr = self._expr()
            return Assign(tok.text, tok.span, expr, (tok.span[0], expr.span[1]))
        expr = self._expr()
        return ExprStmt(expr, expr.span)

    def _expr(self):
        return self._comparison()

    def _comparison(self):
        left = self._conv()
        tok = self._cur()
        if tok.kind == "OP" and tok.text in _COMPARE_OPS:
            self._advance()
            right = self._conv()
            return Compare(tok.text, left, right, tok.span, (left.span[0], right.span[1]))
        return left

    def _conv(self):
        expr = self._additive()
        if self._at("IDENT", "to"):
            self._advance()
            unit, unit_span = self._unit_expr()
            return Convert(expr, unit, unit_span, (expr.span[0], unit_span[1]))
        return expr

    def _unit_expr(self):
        tok = self._cur()
        if tok.kind != "IDENT" or tok.text == "to":
            raise ParseError("expected a unit after 'to'", tok.span)
        first = self._lookup_unit(tok)
        self._advance()
        name, dim, scale = first.name, first.dim, first.scale
        end = tok.span[1]
        compound = False
        while self._at_op("*", "/"):
            op = self._advance()
            utok = self._cur()
            if utok.kind != "IDENT" or utok.text == "to":
                raise ParseError("expected a unit in compound unit", utok.span)
            other = self._lookup_unit(utok)
            if first.affine or other.affine:
                raise ParseError(
                    "absolute temperature unit cannot appear in a compound unit",
                    utok.span,
                )
            self._advance()
            compound = True
            sign = 1 if op.text == "*" else -1
            dim = dim.combine(other.dim, sign)
            scale = scale * other.scale if sign == 1 else scale / other.scale
            name += op.text + other.name
            end = utok.span[1]
        if first.affine and compound:
            raise ParseError(
                "absolute temperature unit cannot appear in a compound unit",
                (tok.span[0], end),
            )
        return Unit(name, dim, scale, affine=first.affine, offset=first.offset), (
            tok.span[0],
            end,
        )

    @staticmethod
    def _lookup_unit(tok: Token) -> Unit:
        unit = UNITS.get(tok.text)
        if unit is None:
            raise ParseError(f"unknown unit '{tok.text}'", tok.span)
        return unit

    def _additive(self):
        left = self._multiplicative()
        while self._at_op("+", "-"):
            op = self._advance()
            right = self._multiplicative()
            left = Binary(op.text, left, right, op.span, (left.span[0], right.span[1]))
        return left

    def _multiplicative(self):
        left = self._unary()
        while self._at_op("*", "/"):
            op = self._advance()
            right = self._unary()
            left = Binary(op.text, left, right, op.span, (left.span[0], right.span[1]))
        return left

    def _unary(self):
        if self._at_op("-"):
            op = self._advance()
            operand = self._unary()
            return Unary("-", operand, op.span, (op.span[0], operand.span[1]))
        if self._at_op("+"):
            self._advance()
            return self._unary()
        return self._postfix()

    def _postfix(self):
        node = self._primary()
        if isinstance(node, Number):
            tok = self._cur()
            if tok.kind == "IDENT" and tok.text != "to":
                unit = self._lookup_unit(tok)
                self._advance()
                node = Number(node.value, unit, (node.span[0], tok.span[1]))
        return node

    def _primary(self):
        tok = self._cur()
        if tok.kind == "NUMBER":
            self._advance()
            return Number(Fraction(tok.text), None, tok.span)
        if tok.kind == "IDENT":
            if tok.text == "to":
                raise ParseError("unexpected 'to'", tok.span)
            self._advance()
            return Var(tok.text, tok.span)
        if self._at_op("("):
            self._advance()
            expr = self._expr()
            close = self._cur()
            if not self._at_op(")"):
                raise ParseError("expected ')'", close.span)
            self._advance()
            return expr
        raise ParseError(f"unexpected {tok.text!r}", tok.span)
