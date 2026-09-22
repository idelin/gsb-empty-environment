"""License expression parser with distinct AND / OR / WITH semantics.

Grammar (precedence: WITH > AND > OR)::

    expr      := or_expr
    or_expr   := and_expr (OR and_expr)*
    and_expr  := with_expr (AND with_expr)*
    with_expr := primary (WITH IDENT)?
    primary   := '(' expr ')' | IDENT

Semantics kept deliberately separate:
* AND  - every operand must hold; operands concatenate into one term.
* OR   - exactly one operand must hold; each operand is an alternative
         branch (a solver choice point).
* WITH - attaches an exception to the single license atom on its left.
         It is NOT an unconditional allowance: the exception exists only
         on that atom and only when the containing branch is chosen.

``to_terms`` lowers an expression to DNF: a tuple of alternative terms,
each term a tuple of atoms that must all hold together.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

_KEYWORDS = {"AND", "OR", "WITH"}
_TOKEN_RE = re.compile(r"\s*([()]|[^\s()]+)")


class ExprError(ValueError):
    pass


@dataclass(frozen=True)
class Atom:
    license: str
    exception: str | None = None

    def __str__(self) -> str:
        if self.exception:
            return f"{self.license} WITH {self.exception}"
        return self.license


@dataclass(frozen=True)
class And:
    items: tuple


@dataclass(frozen=True)
class Or:
    items: tuple


def _tokenize(text: str) -> list[str]:
    tokens = []
    pos = 0
    while pos < len(text):
        match = _TOKEN_RE.match(text, pos)
        if not match:
            raise ExprError(f"cannot tokenize at {text[pos:]!r}")
        pos = match.end()
        tokens.append(match.group(1))
    if not tokens:
        raise ExprError("empty license expression")
    return tokens


class _Parser:
    def __init__(self, tokens: list[str]):
        self.tokens = tokens
        self.pos = 0

    def _peek(self) -> str | None:
        return self.tokens[self.pos] if self.pos < len(self.tokens) else None

    def _next(self) -> str:
        tok = self._peek()
        if tok is None:
            raise ExprError("unexpected end of expression")
        self.pos += 1
        return tok

    def _expect_ident(self) -> str:
        tok = self._next()
        if tok in ("(", ")") or tok.upper() in _KEYWORDS:
            raise ExprError(f"expected identifier, got {tok!r}")
        return tok

    def parse(self):
        node = self._or_expr()
        if self._peek() is not None:
            raise ExprError(f"trailing token {self._peek()!r}")
        return node

    def _or_expr(self):
        items = [self._and_expr()]
        while self._peek() is not None and self._peek().upper() == "OR":
            self._next()
            items.append(self._and_expr())
        return items[0] if len(items) == 1 else Or(tuple(items))

    def _and_expr(self):
        items = [self._with_expr()]
        while self._peek() is not None and self._peek().upper() == "AND":
            self._next()
            items.append(self._with_expr())
        return items[0] if len(items) == 1 else And(tuple(items))

    def _with_expr(self):
        node = self._primary()
        if self._peek() is not None and self._peek().upper() == "WITH":
            self._next()
            exception = self._expect_ident()
            if not isinstance(node, Atom):
                raise ExprError("WITH applies to a single license, not a group")
            node = Atom(node.license, exception)
        return node

    def _primary(self):
        tok = self._next()
        if tok == "(":
            node = self._or_expr()
            if self._next() != ")":
                raise ExprError("missing closing parenthesis")
            return node
        if tok == ")" or tok.upper() in _KEYWORDS:
            raise ExprError(f"unexpected token {tok!r}")
        return Atom(tok)


def parse(text: str):
    """Parse a license expression into an AST of Atom/And/Or nodes."""
    return _Parser(_tokenize(text)).parse()


def to_terms(node) -> tuple[tuple[Atom, ...], ...]:
    """Lower an AST to DNF: alternative terms, each a tuple of atoms.

    OR contributes alternatives, AND concatenates, WITH stays attached
    to its atom inside the term. Duplicate terms are collapsed.
    """
    if isinstance(node, Atom):
        return ((node,),)
    if isinstance(node, Or):
        terms: list[tuple[Atom, ...]] = []
        for item in node.items:
            terms.extend(to_terms(item))
    elif isinstance(node, And):
        terms = [()]
        for item in node.items:
            terms = [left + right for left in terms for right in to_terms(item)]
    else:  # pragma: no cover - defensive
        raise TypeError(f"unknown node {node!r}")
    seen: dict[tuple[Atom, ...], None] = {}
    for term in terms:
        seen.setdefault(tuple(sorted(set(term), key=str)), None)
    return tuple(seen)


def parse_terms(text: str) -> tuple[tuple[Atom, ...], ...]:
    """Parse an expression straight into its alternative terms."""
    return to_terms(parse(text))
