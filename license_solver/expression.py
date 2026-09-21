"""License expressions: AND, OR and WITH are distinct node types.

- AND: obligations of both sides apply (union).
- OR: a choice point; the solver picks exactly one branch, globally.
- WITH: a license coupled with an exception. The exception modifies
  obligations only while its condition holds for the target; it is never
  split into an unconditional allowance of the base license.
"""

from __future__ import annotations

import re
from dataclasses import dataclass


@dataclass(frozen=True)
class License:
    name: str


@dataclass(frozen=True)
class With:
    license: License
    exception: str


@dataclass(frozen=True)
class And:
    left: object
    right: object


@dataclass(frozen=True)
class Or:
    left: object
    right: object


_TOKEN_RE = re.compile(r"\(|\)|[^\s()]+")
_KEYWORDS = {"AND", "OR", "WITH"}


def tokenize(text: str) -> list[str]:
    tokens = _TOKEN_RE.findall(text)
    if not tokens:
        raise ValueError("empty license expression")
    return tokens


def parse(text: str):
    """Parse an expression. Precedence: WITH > AND > OR."""
    parser = _Parser(tokenize(text))
    node = parser.parse_or()
    if parser.peek() is not None:
        raise ValueError(f"unexpected token {parser.peek()!r} in {text!r}")
    return node


class _Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.pos = 0

    def peek(self):
        return self.tokens[self.pos] if self.pos < len(self.tokens) else None

    def next(self):
        token = self.peek()
        self.pos += 1
        return token

    def parse_or(self):
        node = self.parse_and()
        while self.peek() is not None and self.peek().upper() == "OR":
            self.next()
            node = Or(node, self.parse_and())
        return node

    def parse_and(self):
        node = self.parse_with()
        while self.peek() is not None and self.peek().upper() == "AND":
            self.next()
            node = And(node, self.parse_with())
        return node

    def parse_with(self):
        node = self.parse_atom()
        if self.peek() is not None and self.peek().upper() == "WITH":
            self.next()
            exception = self.next()
            if (
                exception is None
                or exception in ("(", ")")
                or exception.upper() in _KEYWORDS
            ):
                raise ValueError("WITH must be followed by an exception name")
            if not isinstance(node, License):
                raise ValueError(
                    "WITH must attach to a single license, not a compound expression"
                )
            node = With(node, exception)
        return node

    def parse_atom(self):
        token = self.next()
        if token is None:
            raise ValueError("unexpected end of expression")
        if token == "(":
            node = self.parse_or()
            if self.next() != ")":
                raise ValueError("missing closing parenthesis")
            return node
        if token == ")" or token.upper() in _KEYWORDS:
            raise ValueError(f"unexpected token {token!r}")
        return License(token)


def or_nodes(node) -> list:
    """All OR choice points, in deterministic left-to-right order."""
    found = []

    def walk(current):
        if isinstance(current, Or):
            found.append(current)
            walk(current.left)
            walk(current.right)
        elif isinstance(current, And):
            walk(current.left)
            walk(current.right)

    walk(node)
    return found


@dataclass(frozen=True)
class EffectiveLicense:
    name: str
    obligations: frozenset
    tags: frozenset
    notes: tuple = ()


def evaluate(node, choices, pos, attributes, licenses, exceptions):
    """Evaluate an expression with OR branches fixed by ``choices``.

    ``choices[i]`` selects the branch of the i-th OR node (left-to-right).
    ``attributes`` are target attributes used to test WITH-exception
    conditions. Returns ``(effective_licenses, next_pos)``.
    """
    if isinstance(node, License):
        info = licenses.get(node.name)
        if info is None:
            raise ValueError(f"unknown license {node.name!r}")
        return [
            EffectiveLicense(node.name, frozenset(info.obligations), frozenset(info.tags))
        ], pos
    if isinstance(node, With):
        (base,), pos = evaluate(
            node.license, choices, pos, attributes, licenses, exceptions
        )
        info = exceptions.get(node.exception)
        if info is None:
            raise ValueError(f"unknown exception {node.exception!r}")
        if attributes.get(info.condition):
            obligations = base.obligations - info.removes_obligations
            tags = base.tags - info.removes_tags
            note = f"{node.exception} applied (condition {info.condition!r} holds)"
        else:
            obligations, tags = base.obligations, base.tags
            note = (
                f"{node.exception} NOT applied (condition {info.condition!r} unmet); "
                "base license obligations stand"
            )
        return [EffectiveLicense(base.name, obligations, tags, (note,))], pos
    if isinstance(node, And):
        left, pos = evaluate(node.left, choices, pos, attributes, licenses, exceptions)
        right, pos = evaluate(node.right, choices, pos, attributes, licenses, exceptions)
        return left + right, pos
    if isinstance(node, Or):
        branch = choices[pos]
        chosen = node.left if branch == 0 else node.right
        return evaluate(chosen, choices, pos + 1, attributes, licenses, exceptions)
    raise TypeError(f"unknown expression node {node!r}")
