"""Columnar batches with per-column statistics used for block pruning.

Statistics contract (this is what makes pruning sound):
  * min/max are computed ONLY over valid values (non-null, non-NaN).
    NaN is never allowed to contaminate min/max, because IEEE comparisons
    with NaN are all False, which would make min/max meaningless and could
    "prove" emptiness incorrectly.
  * null_count and nan_count are tracked separately: null != NaN.
"""

from __future__ import annotations

import dataclasses
from typing import Any, Iterator

from .types import Schema, TypeMismatchError, is_nan


@dataclasses.dataclass(frozen=True)
class ColumnStats:
    count: int          # total rows
    null_count: int     # rows that are None
    nan_count: int      # rows that are float NaN (NOT null)
    valid_count: int    # rows that are neither null nor NaN
    min: Any            # min over valid values; None iff valid_count == 0
    max: Any            # max over valid values; None iff valid_count == 0

    @staticmethod
    def compute(values: list[Any]) -> "ColumnStats":
        count = len(values)
        null_count = 0
        nan_count = 0
        lo = hi = None
        valid = 0
        for v in values:
            if v is None:
                null_count += 1
            elif is_nan(v):
                nan_count += 1
            else:
                valid += 1
                if lo is None or v < lo:
                    lo = v
                if hi is None or v > hi:
                    hi = v
        return ColumnStats(
            count=count,
            null_count=null_count,
            nan_count=nan_count,
            valid_count=valid,
            min=lo,
            max=hi,
        )


class Batch:
    """A typed columnar batch: a Schema plus one value list per column."""

    def __init__(self, schema: Schema, columns: dict[str, list[Any]]) -> None:
        if set(columns) != set(schema.names):
            raise TypeMismatchError(
                f"batch columns {sorted(columns)} != schema {list(schema.names)}"
            )
        lengths = {len(v) for v in columns.values()}
        if len(lengths) > 1:
            raise ValueError("all columns must have the same length")
        for name, values in columns.items():
            typ = schema.type_of(name)
            for i, v in enumerate(values):
                typ.check_value(v, where=f"column {name!r} row {i}")
        self.schema = schema
        self.columns = {name: list(values) for name, values in columns.items()}
        self._stats = {
            name: ColumnStats.compute(values) for name, values in columns.items()
        }

    @property
    def num_rows(self) -> int:
        if not self.columns:
            return 0
        return len(next(iter(self.columns.values())))

    def stats(self, column: str) -> ColumnStats:
        return self._stats[column]

    def rows(self) -> Iterator[dict[str, Any]]:
        for i in range(self.num_rows):
            yield {name: vals[i] for name, vals in self.columns.items()}
