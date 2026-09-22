"""Boolean expressions over feature names: ``and``, ``or``, ``not``, parentheses."""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Callable

from .errors import ExprError

_TOKEN_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_-]*|[()]")
_KEYWORDS = ("and", "or", "not")


class Node:
    """Base class of the expression AST."""


@dataclass(frozen=True)
class Name(Node):
    name: str


@dataclass(frozen=True)
class And(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Or(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Not(Node):
    operand: Node


def _tokenize(text: str) -> list[str]:
    tokens: list[str] = []
    pos = 0
    while pos < len(text):
        if text[pos].isspace():
            pos += 1
            continue
        match = _TOKEN_RE.match(text, pos)
        if not match:
            raise ExprError(f"unexpected character {text[pos]!r} in expression {text!r}")
        tokens.append(match.group(0))
        pos = match.end()
    return tokens


class _Parser:
    def __init__(self, tokens: list[str]):
        self._tokens = tokens
        self._pos = 0

    def peek(self) -> str | None:
        if self._pos < len(self._tokens):
            return self._tokens[self._pos]
        return None

    def advance(self) -> str | None:
        token = self.peek()
        self._pos += 1
        return token

    def parse_or(self) -> Node:
        node = self.parse_and()
        while self.peek() == "or":
            self.advance()
            node = Or(node, self.parse_and())
        return node

    def parse_and(self) -> Node:
        node = self.parse_not()
        while self.peek() == "and":
            self.advance()
            node = And(node, self.parse_not())
        return node

    def parse_not(self) -> Node:
        if self.peek() == "not":
            self.advance()
            return Not(self.parse_not())
        return self.parse_atom()

    def parse_atom(self) -> Node:
        token = self.peek()
        if token == "(":
            self.advance()
            node = self.parse_or()
            if self.advance() != ")":
                raise ExprError("missing closing parenthesis")
            return node
        if token is None or token in _KEYWORDS or token == ")":
            raise ExprError(f"unexpected token {token!r}")
        self.advance()
        return Name(token)


def parse(text: str) -> Node:
    if not isinstance(text, str) or not text.strip():
        raise ExprError("expression must be a non-empty string")
    parser = _Parser(_tokenize(text))
    node = parser.parse_or()
    trailing = parser.peek()
    if trailing is not None:
        raise ExprError(f"unexpected trailing token {trailing!r}")
    return node


def evaluate(node: Node, is_active: Callable[[str], bool]) -> bool:
    if isinstance(node, Name):
        return bool(is_active(node.name))
    if isinstance(node, And):
        return evaluate(node.left, is_active) and evaluate(node.right, is_active)
    if isinstance(node, Or):
        return evaluate(node.left, is_active) or evaluate(node.right, is_active)
    if isinstance(node, Not):
        return not evaluate(node.operand, is_active)
    raise ExprError(f"unknown expression node {node!r}")


def names(node: Node) -> set[str]:
    found: set[str] = set()

    def walk(current: Node) -> None:
        if isinstance(current, Name):
            found.add(current.name)
        elif isinstance(current, (And, Or)):
            walk(current.left)
            walk(current.right)
        elif isinstance(current, Not):
            walk(current.operand)

    walk(node)
    return found


def render(node: Node) -> str:
    if isinstance(node, Name):
        return node.name
    if isinstance(node, Not):
        return f"not {_render_operand(node.operand)}"
    if isinstance(node, And):
        return f"{_render_operand(node.left)} and {_render_operand(node.right)}"
    if isinstance(node, Or):
        return f"{_render_operand(node.left)} or {_render_operand(node.right)}"
    raise ExprError(f"unknown expression node {node!r}")


def _render_operand(node: Node) -> str:
    if isinstance(node, (Name, Not)):
        return render(node)
    return f"({render(node)})"
