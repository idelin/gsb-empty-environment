"""语法分析：把 token 流解析为通用语法树（Form/Sym/IntLit/StrLit）。"""
from __future__ import annotations

from .errors import ParseError
from .lexer import Token, lex
from .nodes import Form, IntLit, Node, Span, StrLit, Sym


def parse(source: str) -> list[Node]:
    """解析源码，返回顶层形式列表。"""
    return parse_tokens(lex(source))


def parse_tokens(tokens: list[Token]) -> list[Node]:
    pos = 0

    def parse_expr() -> Node:
        nonlocal pos
        tok = tokens[pos]
        if tok.kind == "int":
            pos += 1
            return IntLit(span=tok.span, value=int(tok.text))
        if tok.kind == "string":
            pos += 1
            return StrLit(span=tok.span, value=tok.text)
        if tok.kind == "sym":
            pos += 1
            return Sym(span=tok.span, name=tok.text)
        if tok.kind == "lparen":
            pos += 1
            items: list[Node] = []
            while True:
                if pos >= len(tokens):
                    raise ParseError("unclosed '('", tok.span)
                closing = tokens[pos]
                if closing.kind == "rparen":
                    pos += 1
                    return Form(
                        span=Span(tok.span.start, closing.span.end), items=items
                    )
                items.append(parse_expr())
        raise ParseError(f"unexpected token {tok.text!r}", tok.span)

    forms: list[Node] = []
    while pos < len(tokens):
        forms.append(parse_expr())
    return forms
