"""Incremental long-to-wide pivot engine (pure standard library).

Typed input batches are pivoted by a key column and a category column.
The category set may grow at any time; column order is stable (first-seen
order) and every schema change is emitted as an event so that previously
emitted batches can be aligned to the new schema.

Semantics:
  * ``None`` (null) and ``NaN`` are distinct values. Nulls are skipped by
    ``sum``/``count``; ``NaN`` is a real float value and poisons ``sum``.
  * Integer values are aggregated exactly (never widened to float).
  * Rows may be retracted with op ``"-"``; a retraction must match a live
    previously-inserted row (key, category, value).
  * Schema only grows: retracting every cell of a category keeps its column.

No query engine (pandas/sqlite/...) is used -- plain dicts only.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any, Iterable, Optional

__all__ = [
    "Batch",
    "BatchResult",
    "PivotEngine",
    "SchemaChanged",
    "WideTable",
]

VALUE_TYPES = ("int", "float", "str", "bool")
AGGREGATIONS = ("sum", "count")

_NAN = object()  # multiset key for NaN (NaN != NaN, so it cannot key a dict)


def _is_nan(value: Any) -> bool:
    return isinstance(value, float) and math.isnan(value)


def _norm(value: Any) -> Any:
    return _NAN if _is_nan(value) else value


def _check_value_type(value: Any, col_type: str, role: str = "value") -> None:
    if value is None:
        return  # null is allowed for any declared type
    if col_type == "int":
        ok = isinstance(value, int) and not isinstance(value, bool)
    elif col_type == "float":
        ok = isinstance(value, (int, float)) and not isinstance(value, bool)
    elif col_type == "str":
        ok = isinstance(value, str)
    elif col_type == "bool":
        ok = isinstance(value, bool)
    else:
        raise ValueError(f"unknown column type: {col_type!r}")
    if not ok:
        raise TypeError(
            f"{role} {value!r} does not match declared type {col_type!r}"
        )


class _Cell:
    """Live multiset of values for one (key, category) cell."""

    __slots__ = ("values",)

    def __init__(self) -> None:
        self.values: dict[Any, int] = {}

    def add(self, value: Any) -> None:
        key = _norm(value)
        self.values[key] = self.values.get(key, 0) + 1

    def retract(self, value: Any) -> None:
        key = _norm(value)
        count = self.values.get(key, 0)
        if count == 0:
            raise KeyError(f"no live row with value {value!r} to retract")
        if count == 1:
            del self.values[key]
        else:
            self.values[key] = count - 1

    def aggregate(self, agg: str) -> Any:
        if agg == "count":
            return sum(n for v, n in self.values.items() if v is not None)
        # sum: null skipped; all-null -> null; any NaN -> NaN; ints exact.
        int_total = 0
        float_total = 0.0
        has_float = False
        has_value = False
        for v, n in self.values.items():
            if v is None:
                continue
            if v is _NAN:
                return float("nan")
            has_value = True
            if isinstance(v, float):
                has_float = True
                float_total += v * n
            else:
                int_total += v * n
        if not has_value:
            return None
        return int_total + float_total if has_float else int_total


@dataclass(frozen=True)
class Batch:
    """One typed input batch.

    ``rows`` is an iterable of ``(key, category, value)`` inserts or
    ``(op, key, category, value)`` where op is ``"+"`` (insert) or
    ``"-"`` (retract a previously inserted live row).
    """

    key_type: str
    category_type: str
    value_type: str
    rows: Iterable


@dataclass(frozen=True)
class SchemaChanged:
    """Output-schema change event: new category columns were appended."""

    version: int
    added_columns: tuple
    columns: tuple  # full category-column list at `version`


@dataclass(frozen=True)
class BatchResult:
    batch_id: int
    schema_version: int
    added_columns: tuple
    columns: tuple  # category columns at this batch's schema version
    touched_keys: tuple

    @property
    def schema_changed(self) -> bool:
        return bool(self.added_columns)


@dataclass(frozen=True)
class WideTable:
    columns: tuple  # ("key", <category...>) in stable first-seen order
    rows: tuple  # tuple of tuples aligned with `columns`

    def to_dicts(self) -> list:
        return [dict(zip(self.columns, row)) for row in self.rows]


class PivotEngine:
    """Stateful pivot engine: apply typed batches, get wide snapshots."""

    def __init__(self, agg: str = "sum") -> None:
        if agg not in AGGREGATIONS:
            raise ValueError(f"unknown aggregation: {agg!r}")
        self.agg = agg
        self._columns: list = []            # categories, first-seen order
        self._col_pos: dict = {}
        self._cells: dict = {}              # key -> {category: _Cell}
        self._events: list = []
        self._versions: list = [()]         # schema history, index = version
        self._batch_count = 0

    @property
    def columns(self) -> tuple:
        return tuple(self._columns)

    @property
    def schema_version(self) -> int:
        return len(self._versions) - 1

    @property
    def events(self) -> tuple:
        return tuple(self._events)

    def apply(self, batch: Batch) -> BatchResult:
        for declared in (batch.key_type, batch.category_type, batch.value_type):
            if declared not in VALUE_TYPES:
                raise ValueError(f"unknown column type: {declared!r}")
        if self.agg == "sum" and batch.value_type not in ("int", "float"):
            raise TypeError("sum aggregation requires an int or float value column")

        # Phase 1: validate the whole batch (atomic -- a failing batch
        # leaves state and schema untouched). Retractions are checked
        # against live state plus an overlay of earlier rows in this batch.
        rows = []
        overlay: dict = {}
        for raw in batch.rows:
            op, key, category, value = self._normalize_row(raw)
            self._validate_dimension(key, batch.key_type, "key")
            self._validate_dimension(category, batch.category_type, "category")
            _check_value_type(value, batch.value_type)
            marker = (key, category, _norm(value))
            if op == "-":
                cell = self._cells.get(key, {}).get(category)
                live = (cell.values.get(marker[2], 0) if cell else 0)
                live += overlay.get(marker, 0)
                if live <= 0:
                    raise KeyError(
                        f"no live row ({key!r}, {category!r}, {value!r}) to retract"
                    )
            overlay[marker] = overlay.get(marker, 0) + (1 if op == "+" else -1)
            rows.append((op, key, category, value))

        # Phase 2: apply; nothing can fail from here on.
        added: list = []
        touched: dict = {}
        for op, key, category, value in rows:
            if category not in self._col_pos:
                self._col_pos[category] = len(self._columns)
                self._columns.append(category)
                added.append(category)

            cellmap = self._cells.get(key)
            if op == "+":
                if cellmap is None:
                    cellmap = self._cells[key] = {}
                cell = cellmap.get(category)
                if cell is None:
                    cell = cellmap[category] = _Cell()
                cell.add(value)
            else:
                if cellmap is None or category not in cellmap:
                    raise KeyError(
                        f"no live row ({key!r}, {category!r}, {value!r}) to retract"
                    )
                cellmap[category].retract(value)
                if not cellmap[category].values:
                    del cellmap[category]
                    if not cellmap:
                        del self._cells[key]
            touched[key] = None

        if added:
            self._versions.append(tuple(self._columns))
            self._events.append(
                SchemaChanged(
                    version=len(self._versions) - 1,
                    added_columns=tuple(added),
                    columns=tuple(self._columns),
                )
            )
        self._batch_count += 1
        return BatchResult(
            batch_id=self._batch_count,
            schema_version=len(self._versions) - 1,
            added_columns=tuple(added),
            columns=tuple(self._columns),
            touched_keys=tuple(touched),
        )

    def snapshot(self) -> WideTable:
        """Current wide table; missing cells are null (None)."""
        cols = tuple(self._columns)
        rows = []
        for key, cellmap in self._cells.items():
            row = [key] + [None] * len(cols)
            for category, cell in cellmap.items():
                row[1 + self._col_pos[category]] = cell.aggregate(self.agg)
            rows.append(tuple(row))
        return WideTable(columns=("key",) + cols, rows=tuple(rows))

    def align_values(
        self,
        values: Iterable,
        from_version: int,
        to_version: Optional[int] = None,
    ) -> list:
        """Re-align category values emitted under `from_version` to
        `to_version` (default: current schema); new columns get null."""
        versions = self._versions
        if not 0 <= from_version < len(versions):
            raise ValueError(f"unknown schema version: {from_version!r}")
        if to_version is None:
            dst = tuple(self._columns)
        else:
            if not 0 <= to_version < len(versions):
                raise ValueError(f"unknown schema version: {to_version!r}")
            dst = versions[to_version]
        src = versions[from_version]
        values = list(values)
        if len(values) != len(src):
            raise ValueError(
                f"expected {len(src)} values for schema v{from_version}, "
                f"got {len(values)}"
            )
        by_category = dict(zip(src, values))
        return [by_category.get(c) for c in dst]

    @staticmethod
    def _normalize_row(raw) -> tuple:
        if len(raw) == 3:
            return "+", raw[0], raw[1], raw[2]
        if len(raw) == 4:
            if raw[0] not in ("+", "-"):
                raise ValueError(f"op must be '+' or '-', got {raw[0]!r}")
            return raw[0], raw[1], raw[2], raw[3]
        raise ValueError(
            "row must be (key, category, value) or (op, key, category, value)"
        )

    @staticmethod
    def _validate_dimension(value: Any, col_type: str, role: str) -> None:
        if value is None:
            raise ValueError(f"{role} must not be null")
        if _is_nan(value):
            raise ValueError(f"{role} must not be NaN")
        _check_value_type(value, col_type, role)
        try:
            hash(value)
        except TypeError:
            raise TypeError(f"{role} must be hashable: {value!r}") from None
