"""AST nodes for the regexlite engine.

Every node carries ``span = (start, end)``: the half-open offset range of the
pattern source text that produced the node.  Spans are preserved all the way
into the NFA so that automaton states can be traced back to source positions.
"""
from __future__ import annotations

from dataclasses import dataclass

Span = tuple[int, int]


@dataclass(frozen=True)
class Node:
    span: Span


@dataclass(frozen=True)
class Empty(Node):
    """Matches the empty string (epsilon).  Distinct from an empty CharClass,
    which matches nothing at all (the empty language)."""


@dataclass(frozen=True)
class Literal(Node):
    char: str


@dataclass(frozen=True)
class CharClass(Node):
    """``[a-z]`` style class.  ``ranges`` are inclusive char pairs; ``negated``
    complements the set against the explicit alphabet at compile time.

    An empty, non-negated class (``[]``) is the empty language: it matches no
    string, not even the empty one.
    """
    ranges: tuple[tuple[str, str], ...]
    negated: bool = False


@dataclass(frozen=True)
class Dot(Node):
    """Any single symbol of the explicit alphabet."""


@dataclass(frozen=True)
class Concat(Node):
    parts: tuple[Node, ...]


@dataclass(frozen=True)
class Alt(Node):
    """Ordered alternation: earlier branches have higher priority."""
    branches: tuple[Node, ...]


@dataclass(frozen=True)
class Star(Node):
    """Greedy star.  Empty iterations are forbidden (a star body that matches
    the empty string terminates the loop instead of spinning forever)."""
    child: Node


@dataclass(frozen=True)
class Group(Node):
    """Capturing group.  ``label`` is either a user supplied name
    (``(?<name>...)`` / ``(?P<name>...)``) or the decimal string of the
    group's 1-based index for plain ``(...)`` groups."""
    label: str
    child: Node
