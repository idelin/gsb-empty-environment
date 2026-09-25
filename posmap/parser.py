"""展开后文本的递归下降语法分析（自行实现，不使用编译器）。"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Span


class ParseError(Exception):
    def __init__(self, message: str, span: Span | None = None):
        super().__init__(message)
        self.span = span


@dataclass
class Num:
    value: int
    span: Span


@dataclass
class Var:
    name: str
    span: Span


@dataclass
class Neg:
    operand: object
    op_span: Span
    span: Span


@dataclass
class BinOp:
    op: str
    left: object
    right: object
    op_span: Span
    span: Span


@dataclass
class LetStmt:
    name: str
    name_span: Span
    expr: object
    span: Span


@dataclass
class PrintStmt:
    expr: object
    kw_span: Span
    span: Span


def _span(tok) -> Span:
    return Span("expanded", tok.start, tok.end)


class _Parser:
    def __init__(self, toks):
        self.toks = toks
        self.pos = 0

    def peek(self):
        return self.toks[self.pos] if self.pos < len(self.toks) else None

    def expect(self, text: str):
        t = self.peek()
        if t is None or t.text != text:
            got = repr(t.text) if t else "EOF"
            raise ParseError(f"期望 {text!r}，实际 {got}", _span(t) if t else None)
        self.pos += 1
        return t

    def parse_expr(self):
        node = self.parse_term()
        while True:
            t = self.peek()
            if t is None or t.text not in ("+", "-"):
                return node
            self.pos += 1
            right = self.parse_term()
            node = BinOp(t.text, node, right, _span(t),
                         Span("expanded", node.span.start, right.span.end))

    def parse_term(self):
        node = self.parse_unary()
        while True:
            t = self.peek()
            if t is None or t.text not in ("*", "/"):
                return node
            self.pos += 1
            right = self.parse_unary()
            node = BinOp(t.text, node, right, _span(t),
                         Span("expanded", node.span.start, right.span.end))

    def parse_unary(self):
        t = self.peek()
        if t is not None and t.text == "-":
            self.pos += 1
            operand = self.parse_unary()
            return Neg(operand, _span(t),
                       Span("expanded", t.start, operand.span.end))
        return self.parse_primary()

    def parse_primary(self):
        t = self.peek()
        if t is None:
            raise ParseError("意外结束")
        if t.kind == "INT":
            self.pos += 1
            return Num(int(t.text), _span(t))
        if t.kind == "IDENT":
            self.pos += 1
            return Var(t.text, _span(t))
        if t.text == "(":
            self.pos += 1
            node = self.parse_expr()
            self.expect(")")
            return node
        raise ParseError(f"无法解析的 token {t.text!r}", _span(t))


def parse_statement(toks):
    parser = _Parser(toks)
    first = parser.peek()
    if first is None:
        raise ParseError("空语句")
    if first.text == "let":
        parser.pos += 1
        name = parser.peek()
        if name is None or name.kind != "IDENT":
            raise ParseError("'let' 后应为标识符", _span(name) if name else None)
        parser.pos += 1
        parser.expect("=")
        expr = parser.parse_expr()
        semi = parser.expect(";")
        stmt = LetStmt(name.text, _span(name), expr,
                       Span("expanded", first.start, semi.end))
    elif first.text == "print":
        parser.pos += 1
        expr = parser.parse_expr()
        semi = parser.expect(";")
        stmt = PrintStmt(expr, _span(first),
                         Span("expanded", first.start, semi.end))
    else:
        raise ParseError(f"未知语句起点 {first.text!r}", _span(first))
    if parser.pos != len(toks):
        extra = parser.peek()
        raise ParseError("语句末尾有多余 token", _span(extra))
    return stmt
