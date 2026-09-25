"""Aggregate function states with NULL/NaN-aware semantics.

Semantics (documented and relied upon by the tests):

* ``NULL`` values are skipped by every aggregate.
* ``NaN`` is a real value, distinct from ``NULL``:
    - ``COUNT`` counts it (it is not NULL).
    - ``SUM`` yields ``NaN`` if any non-null input was ``NaN``.
    - ``MIN``/``MAX`` yield ``NaN`` if any non-null input was ``NaN``.
* Integer arithmetic is exact: summing ints never leaves ``int`` land, so
  values beyond 2**53 (or 2**63) are computed without rounding.
* An aggregate over zero non-null values yields ``NULL``.

Every state object supports ``to_json``/``from_json`` so a partially
aggregated group can be persisted inside a checkpoint.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any

from .types import NULL, decode_value, encode_value, is_nan, is_null


class AggFunc(Enum):
    SUM = "sum"
    COUNT = "count"
    MIN = "min"
    MAX = "max"


@dataclass(frozen=True)
class AggSpec:
    column: str
    func: AggFunc

    def __init__(self, column: str, func: "AggFunc | str") -> None:
        object.__setattr__(self, "column", column)
        object.__setattr__(self, "func", AggFunc(func))

    @property
    def name(self) -> str:
        return f"{self.func.value}_{self.column}"


class AggState:
    """Base class for per-group aggregate state."""

    func: AggFunc

    def add(self, value: Any) -> None:
        raise NotImplementedError

    def result(self) -> Any:
        raise NotImplementedError

    def to_json(self) -> dict:
        raise NotImplementedError

    @classmethod
    def from_json(cls, data: dict) -> "AggState":
        raise NotImplementedError


class SumState(AggState):
    func = AggFunc.SUM

    def __init__(self) -> None:
        self.total: Any = 0
        self.seen = False
        self.has_nan = False

    def add(self, value: Any) -> None:
        if is_null(value):
            return
        self.seen = True
        if is_nan(value):
            self.has_nan = True
            return
        self.total = self.total + value

    def result(self) -> Any:
        if not self.seen:
            return NULL
        if self.has_nan:
            return float("nan")
        return self.total

    def to_json(self) -> dict:
        return {
            "func": self.func.value,
            "seen": self.seen,
            "has_nan": self.has_nan,
            "total": encode_value(self.total),
        }

    @classmethod
    def from_json(cls, data: dict) -> "SumState":
        state = cls()
        state.seen = data["seen"]
        state.has_nan = data["has_nan"]
        state.total = decode_value(data["total"])
        return state


class CountState(AggState):
    func = AggFunc.COUNT

    def __init__(self) -> None:
        self.count = 0

    def add(self, value: Any) -> None:
        if is_null(value):
            return
        self.count += 1

    def result(self) -> Any:
        return self.count

    def to_json(self) -> dict:
        return {"func": self.func.value, "count": self.count}

    @classmethod
    def from_json(cls, data: dict) -> "CountState":
        state = cls()
        state.count = data["count"]
        return state


class _MinMaxState(AggState):
    def __init__(self) -> None:
        self.best: Any = None
        self.seen = False
        self.has_nan = False

    def _better(self, candidate: Any, current: Any) -> bool:
        raise NotImplementedError

    def add(self, value: Any) -> None:
        if is_null(value):
            return
        self.seen = True
        if is_nan(value):
            self.has_nan = True
            return
        if self.best is None or self._better(value, self.best):
            self.best = value

    def result(self) -> Any:
        if not self.seen:
            return NULL
        if self.has_nan:
            return float("nan")
        return self.best

    def to_json(self) -> dict:
        return {
            "func": self.func.value,
            "seen": self.seen,
            "has_nan": self.has_nan,
            "best": encode_value(self.best) if self.best is not None else None,
        }

    @classmethod
    def from_json(cls, data: dict) -> "_MinMaxState":
        state = cls()
        state.seen = data["seen"]
        state.has_nan = data["has_nan"]
        state.best = decode_value(data["best"]) if data["best"] is not None else None
        return state


class MinState(_MinMaxState):
    func = AggFunc.MIN

    def _better(self, candidate: Any, current: Any) -> bool:
        return candidate < current


class MaxState(_MinMaxState):
    func = AggFunc.MAX

    def _better(self, candidate: Any, current: Any) -> bool:
        return candidate > current


_STATE_CLASSES = {
    AggFunc.SUM.value: SumState,
    AggFunc.COUNT.value: CountState,
    AggFunc.MIN.value: MinState,
    AggFunc.MAX.value: MaxState,
}


def make_state(func: AggFunc) -> AggState:
    return _STATE_CLASSES[AggFunc(func).value]()


def state_from_json(data: dict) -> AggState:
    try:
        cls = _STATE_CLASSES[data["func"]]
    except KeyError:
        raise ValueError(f"unknown aggregate function {data.get('func')!r}") from None
    return cls.from_json(data)
