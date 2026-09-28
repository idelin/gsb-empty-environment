"""Query language: predicates, three query kinds, canonical serialization."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Dict, List, Mapping, Sequence, Tuple

from .synthetic_data import SyntheticData, canonical_value

ALLOWED_OPERATORS = frozenset({"==", "!=", ">", ">=", "<", "<=", "in", "not in"})


@dataclass(frozen=True)
class Condition:
    field_name: str
    operator: str
    value: Any

    def __post_init__(self) -> None:
        if not isinstance(self.field_name, str) or not self.field_name:
            raise ValueError("field_name must be a non-empty string")
        if self.operator not in ALLOWED_OPERATORS:
            raise ValueError(f"unsupported operator: {self.operator!r}")
        if self.operator in ("in", "not in"):
            if not isinstance(self.value, (list, tuple, set, frozenset)):
                raise ValueError("'in'/'not in' requires a list/tuple/set of values")
            if len(self.value) == 0:
                raise ValueError("'in'/'not in' value set must be non-empty")

    def matches(self, record: Mapping[str, Any]) -> bool:
        actual = record.get(self.field_name)
        if self.operator == "==":
            return actual == self.value
        if self.operator == "!=":
            return actual != self.value
        if self.operator == ">":
            return actual is not None and actual > self.value
        if self.operator == ">=":
            return actual is not None and actual >= self.value
        if self.operator == "<":
            return actual is not None and actual < self.value
        if self.operator == "<=":
            return actual is not None and actual <= self.value
        if self.operator == "in":
            return actual in self._value_set()
        if self.operator == "not in":
            return actual not in self._value_set()
        raise AssertionError("unreachable")

    def _value_set(self) -> frozenset:
        return frozenset(self.value)

    def canonical(self) -> List[Any]:
        if self.operator in ("in", "not in"):
            encoded = sorted(
                (canonical_value(item) for item in self._value_set()),
                key=lambda p: p[0] + ":" + p[1],
            )
            value_repr: Any = encoded
        else:
            value_repr = canonical_value(self.value)
        return ["cond", self.field_name, self.operator, value_repr]


@dataclass(frozen=True)
class Predicate:
    """Conjunction of conditions; empty predicate matches every record."""

    conditions: Tuple[Condition, ...] = field(default_factory=tuple)

    def __post_init__(self) -> None:
        object.__setattr__(self, "conditions", tuple(self.conditions))

    def matches(self, record: Mapping[str, Any]) -> bool:
        return all(cond.matches(record) for cond in self.conditions)

    def canonical(self) -> List[Any]:
        rendered = sorted(
            (json.dumps(cond.canonical(), sort_keys=True, separators=(",", ":")) for cond in self.conditions)
        )
        return ["and", rendered]


@dataclass(frozen=True)
class CountQuery:
    predicate: Predicate = field(default_factory=Predicate)
    epsilon: float = 1.0
    # Free-form parameter that must be part of semantics but has no effect on
    # the true answer: changing it invalidates reuse (e.g. a publication
    # threshold).
    threshold: float | None = None

    def __post_init__(self) -> None:
        _check_epsilon(self.epsilon)
        if not isinstance(self.predicate, Predicate):
            raise ValueError("predicate must be a Predicate")

    @property
    def kind(self) -> str:
        return "count"

    @property
    def sensitivity(self) -> float:
        return 1.0

    def canonical_payload(self) -> List[Any]:
        return ["count", self.predicate.canonical(), self.epsilon, self.threshold]


@dataclass(frozen=True)
class SumQuery:
    field_name: str
    lower_bound: float
    upper_bound: float
    predicate: Predicate = field(default_factory=Predicate)
    epsilon: float = 1.0
    threshold: float | None = None

    def __post_init__(self) -> None:
        _check_epsilon(self.epsilon)
        if not self.field_name:
            raise ValueError("field_name required")
        if self.lower_bound > self.upper_bound:
            raise ValueError("lower_bound must be <= upper_bound")
        if not isinstance(self.predicate, Predicate):
            raise ValueError("predicate must be a Predicate")

    @property
    def kind(self) -> str:
        return "sum"

    @property
    def sensitivity(self) -> float:
        return float(max(abs(self.lower_bound), abs(self.upper_bound)))

    def canonical_payload(self) -> List[Any]:
        return [
            "sum",
            self.field_name,
            self.lower_bound,
            self.upper_bound,
            self.predicate.canonical(),
            self.epsilon,
            self.threshold,
        ]


@dataclass(frozen=True)
class HistogramQuery:
    field_name: str
    predicate: Predicate = field(default_factory=Predicate)
    epsilon: float = 1.0
    threshold: float | None = None

    def __post_init__(self) -> None:
        _check_epsilon(self.epsilon)
        if not self.field_name:
            raise ValueError("field_name required")
        if not isinstance(self.predicate, Predicate):
            raise ValueError("predicate must be a Predicate")

    @property
    def kind(self) -> str:
        return "histogram"

    @property
    def sensitivity(self) -> float:
        return 1.0

    def canonical_payload(self) -> List[Any]:
        return ["hist", self.field_name, self.predicate.canonical(), self.epsilon, self.threshold]


Query = CountQuery | SumQuery | HistogramQuery


def _check_epsilon(epsilon: float) -> None:
    if not isinstance(epsilon, (int, float)) or isinstance(epsilon, bool):
        raise ValueError("epsilon must be a number")
    if epsilon <= 0 or epsilon == float("inf"):
        raise ValueError("epsilon must be a positive finite number")


def canonical_query_id(query: Query) -> str:
    """Deterministic semantic id; field order and constructor order do not matter."""
    return json.dumps(query.canonical_payload(), sort_keys=True, separators=(",", ":"))


def true_answer(query: Query, data: SyntheticData) -> Any:
    """Noise-free answer, used only for diagnostics on synthetic data."""
    rows = [record for record in data.records if query.predicate.matches(record)]
    if query.kind == "count":
        return float(len(rows))
    if query.kind == "sum":
        return float(
            sum(min(max(float(record[query.field_name]), query.lower_bound), query.upper_bound) for record in rows)
        )
    groups: Dict[str, int] = {}
    for record in rows:
        key = str(record.get(query.field_name))
        groups[key] = groups.get(key, 0) + 1
    return dict(sorted(groups.items()))
