"""语法分析:递归下降,把 token 流解析为 AST(手写,不借助现成编译器)。"""
from __future__ import annotations

import re

from .ast_nodes import Call, Node, Number, Origin, Span, Symbol, Text
from .errors import ParseError
from .lexer import tokenize

_INT_RE = re.compile(r"[+-]?\d+")
_FLOAT_RE = re.compile(r"[+-]?(\d+\.\d*|\.\d+)([eE][+-]?\d+)?")


def parse(source: str) -> list[Node]:
    tokens = tokenize(source)
    pos = 0

    def peek():
        return tokens[pos] if pos < len(tokens) else None

    def parse_expr() -> Node:
        nonlocal pos
        tok = tokens[pos]
        if tok.kind == "lparen":
            pos += 1
            items: list[Node] = []
            while True:
                t = peek()
                if t is None:
                    raise ParseError("unclosed '('", tok.span)
                if t.kind == "rparen":
                    pos += 1
                    return Call(Span(tok.span.start, t.span.end), Origin(), items)
                items.append(parse_expr())
        if tok.kind == "rparen":
            raise ParseError("unexpected ')'", tok.span)
        pos += 1
        if tok.kind == "string":
            return Text(tok.span, Origin(), tok.text)
        if _INT_RE.fullmatch(tok.text):
            return Number(tok.span, Origin(), int(tok.text))
        if _FLOAT_RE.fullmatch(tok.text):
            return Number(tok.span, Origin(), float(tok.text))
        return Symbol(tok.span, Origin(), tok.text)

    nodes: list[Node] = []
    while peek() is not None:
        nodes.append(parse_expr())
    return nodes
