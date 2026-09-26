"""Canonical query specification and synthetic dataset container.

A query specification is serialized deterministically (:meth:`Query.canonical_spec`):
predicate (field) order is irrelevant, while thresholds, operators, clip bounds
and the query kind are all part of the specification.
"""

from __future__ import annotations

import json
import math
from dataclasses import dataclass
from typing import Any, Final, Literal

Column = str
Op = Literal["==", "!=", ">", ">=", "<", "<="]
QueryType = Literal["count", "sum"]

_OPS: Final[frozenset[str]] = frozenset({"==", "!=", ">", ">=", "<", "<="})
_ALLOWED_VALUE_TYPES: Final = (int, float, str, bool)
_ORDERED_OPS: Final = frozenset({">", ">=", "<", "<="})


def _value_token(value: int | float | str | bool) -> str:
    """Type-tagged, round-trippable literal token for canonical serialization."""
    if isinstance(value, bool):
        return f"bool:{str(value).lower()}"
    if isinstance(value, int):
        return f"int:{value}"
    if isinstance(value, float):
        return f"float:{value!r}"
    return f"str:{json.dumps(value, ensure_ascii=False)}"


@dataclass(frozen=True)
class Predicate:
    """A single row-level filter ``column op value``."""

    column: str
    op: Op
    value: int | float | str | bool

    def __post_init__(self) -> None:
        if not isinstance(self.column, str) or not self.column:
            raise ValueError("predicate column must be a non-empty string")
        if self.op not in _OPS:
            raise ValueError(f"unsupported comparison operator: {self.op!r}")
        if not isinstance(self.value, _ALLOWED_VALUE_TYPES):
            raise TypeError("predicate value must be int, float, str, or bool")
        if isinstance(self.value, float) and not math.isfinite(self.value):
            raise ValueError("float predicate values must be finite")

    def key_token(self) -> str:
        return f"({json.dumps(self.column, ensure_ascii=False)}|{self.op}|{_value_token(self.value)})"

    def matches(self, row: dict[str, Any]) -> bool:
        x = row[self.column]
        v = self.value
        if self.op == "==":
            return x == v
        if self.op == "!=":
            return x != v
        try:
            if self.op == ">":
                return x > v
            if self.op == ">=":
                return x >= v
            if self.op == "<":
                return x < v
            return x <= v
        except TypeError:
            return False


@dataclass(frozen=True)
class Query:
    """A scalar counting or bounded-sum query with an optional conjunction of filters.

    ``sum`` queries must declare clipping ``bounds`` (lo, hi): every per-record
    contribution is clipped to that interval, which fixes global sensitivity at
    ``max(|lo|, |hi|)`` under add/remove adjacency.
    """

    kind: QueryType
    column: str | None = None
    predicates: tuple[Predicate, ...] = ()
    bounds: tuple[float, float] | None = None

    def __post_init__(self) -> None:
        if self.kind not in ("count", "sum"):
            raise ValueError(f"query kind must be 'count' or 'sum', got {self.kind!r}")
        if not isinstance(self.predicates, tuple):
            object.__setattr__(self, "predicates", tuple(self.predicates))
        if self.kind == "sum":
            if not isinstance(self.column, str) or not self.column:
                raise ValueError("sum query requires a non-empty column")
            if self.bounds is None:
                raise ValueError("sum query requires clip bounds (lo, hi)")
            lo, hi = self.bounds
            lo, hi = float(lo), float(hi)
            object.__setattr__(self, "bounds", (lo, hi))
            if not (math.isfinite(lo) and math.isfinite(hi)) or lo > hi:
                raise ValueError("bounds must be finite with lo <= hi")
        elif self.column is not None or self.bounds is not None:
            raise ValueError("column and bounds are only valid for sum queries")

    def canonical_spec(self) -> str:
        """Deterministic JSON of the query semantics.

        Predicates are sorted by their tokens, so their construction order does
        not affect the canonical form.
        """
        obj = {
            "kind": self.kind,
            "column": self.column,
            "predicates": sorted(p.key_token() for p in self.predicates),
            "bounds": list(self.bounds) if self.bounds is not None else None,
        }
        return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


@dataclass(frozen=True)
class SyntheticDataset:
    """Synthetic-only data container. Real data is rejected by the engine."""

    name: str
    version: str
    seed: int
    records: tuple[dict[str, Any], ...]
    schema: dict[str, str]
    kind: str = "synthetic"

    def __post_init__(self) -> None:
        if self.kind != "synthetic":
            raise ValueError("only synthetic datasets are supported")
        if not isinstance(self.records, tuple):
            object.__setattr__(self, "records", tuple(self.records))
