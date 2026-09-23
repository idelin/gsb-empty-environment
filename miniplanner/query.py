"""User-facing parameterized query shape."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence

from miniplanner.expr import Expr, Param, expr_key, walk_exprs


@dataclass(frozen=True)
class Query:
    table: str
    predicate: Expr | None = None
    projection: Sequence[str] | None = None
    parameter_count: int | None = None

    def arity(self) -> int:
        if self.parameter_count is not None:
            return self.parameter_count
        if self.predicate is None:
            return 0
        indices = [node.index for node in walk_exprs(self.predicate) if isinstance(node, Param)]
        return max(indices, default=-1) + 1

    def projection_key(self) -> tuple[str, ...] | None:
        return tuple(self.projection) if self.projection is not None else None

    def key(self) -> tuple[object, ...]:
        predicate_key = expr_key(self.predicate) if self.predicate is not None else None
        return (self.table, predicate_key, self.projection_key(), self.arity())
