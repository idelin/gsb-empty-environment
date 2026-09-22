from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .expressions import Expression
from .operators import Node
from .relations import Column, Provenance, Relation, Row, Schema


DISTRIBUTIVE_FUNCTIONS = frozenset({"count", "count_star", "sum", "avg", "min", "max"})


@dataclass(frozen=True)
class AggExpr:
    function: str
    source: Expression | None
    output: Column

    @property
    def state_column(self) -> Column:
        return Column("partial", f"_state_{self.output.name}")


def empty_state(function: str) -> tuple[Any, ...]:
    if function in {"count", "count_star"}:
        return (0,)
    if function == "sum":
        return (0, 0)
    if function == "avg":
        return (0, 0)
    if function in {"min", "max"}:
        return (None,)
    raise ValueError(f"unsupported aggregate {function}")


def update_state(function: str, state: tuple[Any, ...], row: Row, source: Expression | None) -> tuple[Any, ...]:
    if function == "count_star":
        return (state[0] + 1,)
    if source is None:
        raise ValueError(f"{function} requires a source expression")
    value = source.evaluate(row)
    if value is None:
        return state
    if function == "count":
        return (state[0] + 1,)
    if function == "sum":
        return (state[0] + value, state[1] + 1)
    if function == "avg":
        return (state[0] + value, state[1] + 1)
    if function == "min":
        return (value,) if state[0] is None or value < state[0] else state
    if function == "max":
        return (value,) if state[0] is None or value > state[0] else state
    raise ValueError(f"unsupported aggregate {function}")


def merge_states(function: str, left: tuple[Any, ...], right: tuple[Any, ...]) -> tuple[Any, ...]:
    if function in {"count", "count_star"}:
        return (left[0] + right[0],)
    if function == "sum":
        return (left[0] + right[0], left[1] + right[1])
    if function == "avg":
        return (left[0] + right[0], left[1] + right[1])
    if function == "min":
        if left[0] is None:
            return right
        if right[0] is None:
            return left
        return (min(left[0], right[0]),)
    if function == "max":
        if left[0] is None:
            return right
        if right[0] is None:
            return left
        return (max(left[0], right[0]),)
    raise ValueError(f"unsupported aggregate {function}")


def finalize_state(function: str, state: tuple[Any, ...]) -> Any:
    if function in {"count", "count_star"}:
        return state[0]
    if function == "sum":
        return None if state[1] == 0 else state[0]
    if function == "avg":
        return None if state[1] == 0 else state[0] / state[1]
    if function in {"min", "max"}:
        return state[0]
    raise ValueError(f"unsupported aggregate {function}")
