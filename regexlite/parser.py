"""Recursive-descent parser: pattern text -> AST with source spans.

Grammar (precedence low -> high):

    alt    := concat ("|" concat)*        # ordered alternation
    concat := repeat*                     # possibly empty -> Empty node
    repeat := atom "*"*"                  # nested stars allowed: a**
    atom   := "(" alt ")"
            | "(?<" name ">" alt ")"      # named capture
            | "(?P<" name ">" alt ")"     # named capture, Python style
            | "[" "^"? items "]"          # character class ("[]" = empty lang)
            | "."                         # any alphabet symbol
            | "\" char                    # escaped literal
            | char                        # literal

All errors raise ParseError carrying the pattern offset.
"""
from __future__ import annotations

from dataclasses import dataclass

from .ast_nodes import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, Node, Star,
)

_ESCAPES = {"n": "\n", "t": "\t", "r": "\r"}


class ParseError(Exception):
    def __init__(self, message: str, pos: int, pattern: str):
        self.pos = pos
        self.pattern = pattern
        super().__init__(f"{message} (at offset {pos} in {pattern!r})")


@dataclass(frozen=True)
class Parsed:
    pattern: str
    ast: Node
    labels: dict[str, int]  # capture label -> 0-based group index

    @property
    def ngroups(self) -> int:
        return len(self.labels)


class _Parser:
    def __init__(self, pattern: str):
        self.s = pattern
        self.n = len(pattern)
        self.i = 0
        self.labels: dict[str, int] = {}

    # -- helpers ---------------------------------------------------------
    def error(self, message: str, pos: int | None = None) -> None:
        raise ParseError(message, self.i if pos is None else pos, self.s)

    def peek(self) -> str:
        return self.s[self.i] if self.i < self.n else ""

    def take(self) -> str:
        ch = self.s[self.i]
        self.i += 1
        return ch

    def escaped(self) -> str:
        """Read a char after a backslash (backslash already consumed)."""
        if self.i >= self.n:
            self.error("trailing backslash")
        raw = self.take()
        return _ESCAPES.get(raw, raw)

    # -- grammar ---------------------------------------------------------
    def parse_alt(self) -> Node:
        start = self.i
        branches = [self.parse_concat()]
        while self.peek() == "|":
            self.i += 1
            branches.append(self.parse_concat())
        if len(branches) == 1:
            return branches[0]
        return Alt((start, self.i), tuple(branches))

    def parse_concat(self) -> Node:
        start = self.i
        parts = []
        while self.i < self.n and self.peek() not in "|)":
            parts.append(self.parse_repeat())
        if not parts:
            return Empty((start, start))
        if len(parts) == 1:
            return parts[0]
        return Concat((start, self.i), tuple(parts))

    def parse_repeat(self) -> Node:
        node = self.parse_atom()
        while self.peek() == "*":
            self.i += 1
            node = Star((node.span[0], self.i), node)
        return node

    def parse_atom(self) -> Node:
        ch = self.peek()
        if ch == "":
            self.error("unexpected end of pattern")
        if ch == "(":
            return self.parse_group()
        if ch == "[":
            return self.parse_class()
        if ch == ".":
            self.i += 1
            return Dot((self.i - 1, self.i))
        if ch == "*":
            self.error("nothing to repeat")
        if ch in ")|":
            self.error(f"unexpected {ch!r}")
        if ch == "\\":
            self.i += 1
            start = self.i - 1
            lit = self.escaped()
            return Literal((start, self.i), lit)
        self.i += 1
        return Literal((self.i - 1, self.i), ch)

    def parse_group(self) -> Node:
        start = self.i
        self.i += 1  # consume '('
        label: str | None = None
        if self.peek() == "?":
            self.i += 1
            if self.peek() == "P":
                self.i += 1
            if self.peek() == "<":
                self.i += 1
                end = self.s.find(">", self.i)
                if end < 0:
                    self.error("unterminated group name", start)
                label = self.s[self.i:end]
                ok = label and (label[0].isalpha() or label[0] == "_") and all(
                    c.isalnum() or c == "_" for c in label
                )
                if not ok:
                    self.error(f"invalid group name {label!r}", start)
                self.i = end + 1
            else:
                self.error("unsupported group syntax, use (?<name>...) or (?P<name>...)", start)
        idx = len(self.labels)
        if label is None:
            label = str(idx + 1)
        if label in self.labels:
            self.error(f"duplicate group label {label!r}", start)
        self.labels[label] = idx
        child = self.parse_alt()
        if self.peek() != ")":
            self.error("unclosed group", start)
        self.i += 1
        return Group((start, self.i), label, child)

    def parse_class(self) -> Node:
        start = self.i
        self.i += 1  # consume '['
        negated = False
        if self.peek() == "^":
            negated = True
            self.i += 1
        ranges: list[tuple[str, str]] = []
        while True:
            if self.i >= self.n:
                self.error("unterminated character class", start)
            lo = self.take()
            if lo == "]":
                break  # "[]" -> empty class (empty language)
            if lo == "\\":
                lo = self.escaped()
            if self.peek() == "-" and self.i + 1 < self.n and self.s[self.i + 1] != "]":
                self.i += 1  # consume '-'
                hi = self.take()
                if hi == "\\":
                    hi = self.escaped()
                if hi < lo:
                    self.error(f"invalid range {lo!r}-{hi!r}", start)
                ranges.append((lo, hi))
            else:
                ranges.append((lo, lo))
        return CharClass((start, self.i), tuple(ranges), negated)


def parse(pattern: str) -> Parsed:
    """Parse ``pattern`` into a :class:`Parsed` (AST + capture labels)."""
    parser = _Parser(pattern)
    ast = parser.parse_alt()
    if parser.i < parser.n:
        parser.error(f"unexpected {parser.s[parser.i]!r}")
    return Parsed(pattern, ast, dict(parser.labels))
