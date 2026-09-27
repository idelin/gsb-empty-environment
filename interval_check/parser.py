"""Arithmetic expression and assertion parser.

Supported expressions: integer constants, named variables, parentheses,
unary minus, binary ``+`` ``-`` ``*`` and floor division ``//`` (division
rounded toward negative infinity, matching Python's ``//``).

Grammar::

    expr   := term (('+' | '-') term)*
    term   := factor (('*' | '//') factor)*
    factor := ('-' | '+') factor | INT | NAME | '(' expr ')'

Assertions compare an expression against an integer constant with one of
``== != < <= > >=``.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Union


class ParseError(ValueError):
    """Raised on lexing or parsing errors, with a 1-based column."""


@dataclass(frozen=True)
class Node:
    op: str  # 'const' | 'var' | 'neg' | '+' | '-' | '*' | '//'
    value: object = None  # int for const, str for var
    left: "Node | None" = None
    right: "Node | None" = None


_CONST = "const"
_VAR = "var"
_NEG = "neg"
_RELOPS = ("==", "!=", "<=", ">=", "<", ">")

_TOKEN_PUNCT = ("//", "==", "!=", "<=", ">=", "(", ")", "+", "-", "*", "<", ">")


def _tokenize(text: str) -> list[tuple[str, str, int]]:
    tokens: list[tuple[str, str, int]] = []
    i = 0
    n = len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        start = i
        if ch.isdigit():
            i += 1
            while i < n and text[i].isdigit():
                i += 1
            tokens.append(("NUM", text[start:i], start + 1))
            continue
        if ch.isalpha() or ch == "_":
            i += 1
            while i < n and (text[i].isalnum() or text[i] == "_"):
                i += 1
            tokens.append(("NAME", text[start:i], start + 1))
            continue
        if ch == "/":
            if text.startswith("//", i):
                tokens.append(("//", "//", i + 1))
                i += 2
            else:
                raise ParseError(
                    "exact division '/' is not supported; use '//' for "
                    "floor division (column %d)" % (i + 1)
                )
            continue
        for punct in _TOKEN_PUNCT:
            if text.startswith(punct, i):
                tokens.append((punct, punct, i + 1))
                i += len(punct)
                break
        else:
            raise ParseError(f"unexpected character {ch!r} (column {i + 1})")
    tokens.append(("EOF", "", n + 1))
    return tokens


class _Parser:
    def __init__(self, text: str) -> None:
        self.text = text
        self.tokens = _tokenize(text)
        self.pos = 0

    def _peek(self) -> tuple[str, str, int]:
        return self.tokens[self.pos]

    def _advance(self) -> tuple[str, str, int]:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def _expect(self, kind: str) -> tuple[str, str, int]:
        tok = self._peek()
        if tok[0] != kind:
            col = tok[2]
            if kind == "EOF":
                raise ParseError(f"unexpected end of input (column {col})")
            raise ParseError(f"expected {kind!r} but found {tok[1]!r} (column {col})")
        return self._advance()

    def parse_expr(self) -> Node:
        node = self.parse_term()
        while self._peek()[0] in ("+", "-"):
            op = self._advance()[0]
            rhs = self.parse_term()
            node = Node(op=op, left=node, right=rhs)
        return node

    def parse_term(self) -> Node:
        node = self.parse_factor()
        while self._peek()[0] in ("*", "//"):
            op = self._advance()[0]
            rhs = self.parse_factor()
            node = Node(op=op, left=node, right=rhs)
        return node

    def parse_factor(self) -> Node:
        kind, text, col = self._peek()
        if kind in ("+", "-"):
            op = self._advance()[0]
            operand = self.parse_factor()
            if op == "-":
                return Node(op=_NEG, left=operand)
            return operand
        if kind == "NUM":
            self._advance()
            return Node(op=_CONST, value=int(text))
        if kind == "NAME":
            self._advance()
            return Node(op=_VAR, value=text)
        if kind == "(":
            self._advance()
            node = self.parse_expr()
            self._expect(")")
            return node
        if kind == "EOF":
            raise ParseError(f"unexpected end of input (column {col})")
        raise ParseError(f"unexpected token {text!r} (column {col})")


def parse(text: str) -> Node:
    """Parse an arithmetic expression into an AST."""
    if not isinstance(text, str) or not text.strip():
        raise ParseError("empty expression")
    parser = _Parser(text)
    node = parser.parse_expr()
    if parser._peek()[0] != "EOF":
        tok = parser._peek()
        raise ParseError(f"unexpected trailing token {tok[1]!r} (column {tok[2]})")
    return node


@dataclass(frozen=True)
class Assertion:
    """An assertion ``<expression> <relop> <integer constant>``."""

    expr: Node
    relop: str
    rhs: int
    source: str


def parse_assertion(text: str) -> Assertion:
    """Parse an assertion, splitting on the single relational operator."""
    if not isinstance(text, str) or not text.strip():
        raise ParseError("empty assertion")
    # Find the leftmost relational operator; arithmetic '-'/'*' etc. never
    # collide. '=' alone is rejected (only '==' is valid).
    best: tuple[int, str] | None = None
    for rel in _RELOPS:
        idx = text.find(rel)
        if idx != -1 and (best is None or idx < best[0]):
            best = (idx, rel)
    if best is None:
        raise ParseError(
            "assertion must contain one of: ==, !=, <, <=, >, >= "
            "(example: x - y >= 0)"
        )
    idx, rel = best
    lhs_text = text[:idx]
    rhs_text = text[idx + len(rel):]
    # Reject a second relational operator on the right-hand side.
    for other in _RELOPS:
        if other in rhs_text:
            raise ParseError("assertion has more than one relational operator")
    rhs_str = rhs_text.strip()
    if not rhs_str:
        raise ParseError("assertion is missing the right-hand integer constant")
    try:
        rhs = int(rhs_str)
    except ValueError:
        raise ParseError(
            f"right-hand side must be an integer constant, got {rhs_str!r}"
        )
    expr = parse(lhs_text)
    return Assertion(expr=expr, relop=rel, rhs=rhs, source=text)
