"""Query representation: a join graph with inner/outer edges and dependencies."""
from __future__ import annotations

from dataclasses import dataclass

INNER = "inner"
LEFT = "left"
JOIN_KINDS = (INNER, LEFT)


@dataclass(frozen=True)
class JoinEdge:
    """An equi-join predicate left.left_col = right.right_col.

    kind=INNER: the edge commutes freely.
    kind=LEFT (left outer join): not commutative; the preserved (left) side
    must be joined before the nullable (right) side is introduced.
    """

    left: str
    right: str
    left_col: str
    right_col: str
    kind: str = INNER

    def __post_init__(self):
        if self.kind not in JOIN_KINDS:
            raise ValueError(f"unknown join kind {self.kind!r}")

    def precedence(self):
        return ((self.left, self.right),) if self.kind == LEFT else ()

    def connects(self, rel, placed):
        placed = set(placed)
        return (self.left == rel and self.right in placed) or (
            self.right == rel and self.left in placed)


@dataclass(frozen=True)
class Query:
    relations: tuple
    edges: tuple = ()
    # Correlated dependencies: (before, after) precedence pairs restricting
    # the legal join orders (e.g. a lateral-style dependency).
    dependencies: tuple = ()

    def __post_init__(self):
        names = set(self.relations)
        if len(names) != len(self.relations):
            raise ValueError("duplicate relation names")
        for e in self.edges:
            for r in (e.left, e.right):
                if r not in names:
                    raise ValueError(f"edge references unknown relation {r!r}")
        for a, b in self.dependencies:
            if a not in names or b not in names:
                raise ValueError("dependency references unknown relation")

    def precedence_pairs(self):
        pairs = list(self.dependencies)
        for e in self.edges:
            pairs.extend(e.precedence())
        return pairs

    def edges_between(self, rel, placed):
        return [e for e in self.edges if e.connects(rel, placed)]
