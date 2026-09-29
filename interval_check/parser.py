"""Lexer and recursive-descent parser for the interval-check expression language.

Grammar:

    expr     := term (('+' | '-') term)*
    term     := factor (('*' | '//') factor)*
    factor   := INT | NAME | '(' expr ')' | '-' factor
    compare  := expr ('>=' | '<=' | '==' | '!=' | '>' | '<') expr

Integer constants and variable names are the only atoms.  Division is
explicit floor division written as ``//``.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List, Union


@dataclass(frozen=True)
class Int:
    value: int


@dataclass(frozen=True)
class Var:
    name: str


@dataclass(frozen=True)
class Bin:
    op: str
    lhs: "Node"
    rhs: "Node"


@dataclass(frozen=True)
class Neg:
    operand: "Node"


@dataclass(frozen=True)
class Compare:
    op: str
    lhs: "Node"
    rhs: "Node"


Node = Union[Int, Var, Bin, Neg, Compare]

_OPS = {"+": "PLUS", "-": "MINUS", "*": "STAR", "//": "DDIV",
        ">=": "GE", "<=": "LE", "==": "EQ", "!=": "NE", ">": "GT", "<": "LT"}


class ParseError(ValueError):
    """Raised when an expression cannot be tokenized or parsed."""


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    pos: int


def tokenize(text: str) -> List[Token]:
    tokens: List[Token] = []
    i = 0
    n = len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch.isdigit():
            start = i
            while i < n and text[i].isdigit():
                i += 1
            tokens.append(Token("INT", text[start:i], start))
            continue
        if ch.isalpha() or ch == "_":
            start = i
            while i < n and (text[i].isalnum() or text[i] == "_"):
                i += 1
            tokens.append(Token("NAME", text[start:i], start))
            continue
        two = text[i:i + 2]
        if two in _OPS:
            tokens.append(Token(_OPS[two], two, i))
            i += 2
            continue
        if ch in "+-*()":
            kind = {"(": "LPAREN", ")": "RPAREN"}.get(ch, _OPS[ch])
            tokens.append(Token(kind, ch, i))
            i += 1
            continue
        if ch == "/":
            raise ParseError(
                f"位置 {i}: 只支持向下整除 '//'，不支持普通除法 '/'")
        raise ParseError(f"位置 {i}: 无法识别的字符 {ch!r}")
    tokens.append(Token("EOF", "", n))
    return tokens


class _Parser:
    def __init__(self, text: str):
        self.tokens = tokenize(text)
        self.pos = 0

    def peek(self) -> Token:
        return self.tokens[self.pos]

    def next(self) -> Token:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def expect(self, kind: str) -> Token:
        tok = self.peek()
        if tok.kind != kind:
            raise ParseError(
                f"位置 {tok.pos}: 期望 {kind}，实际得到 {tok.text!r}")
        return self.next()

    def parse_expr(self) -> Node:
        node = self.parse_term()
        while self.peek().kind in ("PLUS", "MINUS"):
            op = "+" if self.next().kind == "PLUS" else "-"
            node = Bin(op, node, self.parse_term())
        return node

    def parse_term(self) -> Node:
        node = self.parse_factor()
        while self.peek().kind in ("STAR", "DDIV"):
            op = "*" if self.next().kind == "STAR" else "//"
            node = Bin(op, node, self.parse_factor())
        return node

    def parse_factor(self) -> Node:
        tok = self.peek()
        if tok.kind == "INT":
            self.next()
            return Int(int(tok.text))
        if tok.kind == "NAME":
            self.next()
            return Var(tok.text)
        if tok.kind == "LPAREN":
            self.next()
            node = self.parse_expr()
            self.expect("RPAREN")
            return node
        if tok.kind == "MINUS":
            self.next()
            return Neg(self.parse_factor())
        raise ParseError(
            f"位置 {tok.pos}: 期望整数、变量或 '('，实际得到 {tok.text!r}")

    def parse_compare(self) -> Compare:
        lhs = self.parse_expr()
        tok = self.peek()
        cmp_kinds = {"GE": ">=", "LE": "<=", "EQ": "==",
                     "NE": "!=", "GT": ">", "LT": "<"}
        if tok.kind not in cmp_kinds:
            raise ParseError(
                f"位置 {tok.pos}: 断言必须包含比较运算符 >= <= == != > <")
        self.next()
        rhs = self.parse_expr()
        if self.peek().kind != "EOF":
            extra = self.peek()
            raise ParseError(f"位置 {extra.pos}: 比较后存在多余内容 {extra.text!r}")
        return Compare(cmp_kinds[tok.kind], lhs, rhs)


def parse_expr(text: str) -> Node:
    parser = _Parser(text)
    node = parser.parse_expr()
    if parser.peek().kind != "EOF":
        tok = parser.peek()
        raise ParseError(f"位置 {tok.pos}: 表达式后存在多余内容 {tok.text!r}")
    return node


def parse_compare(text: str) -> Compare:
    return _Parser(text).parse_compare()


def render(node: Node) -> str:
    """Return a canonical fully-parenthesised rendering of a node."""
    if isinstance(node, Int):
        return str(node.value)
    if isinstance(node, Var):
        return node.name
    if isinstance(node, Neg):
        return f"(-{render(node.operand)})"
    if isinstance(node, Compare):
        return f"{render(node.lhs)} {node.op} {render(node.rhs)}"
    return f"({render(node.lhs)} {node.op} {render(node.rhs)})"
