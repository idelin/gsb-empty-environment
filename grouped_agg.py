"""Incremental grouped aggregation (count / sum / min) with row-level updates.

Design notes
------------
* Input arrives as typed columnar batches (:class:`Batch`).  Column types are
  declared up front and every value is validated on the way in.
* ``None`` is SQL-style NULL; ``float('nan')`` is a *different* value.  NULLs
  are excluded from ``sum``/``min`` but still counted by ``count(*)``.
  NaN participates in ``sum`` (propagates) and sorts after every non-NaN
  number for ``min``.
* Integers use Python arbitrary-precision ints, so sums are exact.
* Each group keeps a sorted multiset of its non-null values, so deleting the
  current minimum immediately exposes the next-smallest value.
* A group whose row count drops to zero is reclaimed (removed from the
  result and its bookkeeping freed).
* All mutations go through :meth:`GroupedAggregate.apply_ops`, which
  validates the whole batch of operations *before* applying any of them,
  so a failing batch leaves the table untouched and a row moving from
  group A to group B is migrated atomically (never double counted).
"""

from __future__ import annotations

import bisect
import math
from dataclasses import dataclass
from enum import Enum
from typing import Any, Hashable, Iterable, Iterator, Sequence


# ---------------------------------------------------------------------------
# Column types
# ---------------------------------------------------------------------------

class ColType(Enum):
    INT = "int"
    FLOAT = "float"
    STR = "str"
    BOOL = "bool"


def _matches(col_type: ColType, value: Any) -> bool:
    if col_type is ColType.INT:
        return isinstance(value, int) and not isinstance(value, bool)
    if col_type is ColType.FLOAT:
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if col_type is ColType.STR:
        return isinstance(value, str)
    if col_type is ColType.BOOL:
        return isinstance(value, bool)
    raise AssertionError(f"unknown column type: {col_type!r}")


class _Unset:
    """Sentinel distinguishing 'argument not given' from an explicit NULL."""

    _instance: "_Unset | None" = None

    def __new__(cls) -> "_Unset":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return "UNSET"

    def __bool__(self) -> bool:
        return False


UNSET = _Unset()


# ---------------------------------------------------------------------------
# Typed input batches
# ---------------------------------------------------------------------------

class Batch:
    """A typed columnar batch of input rows."""

    def __init__(self, schema: dict[str, ColType], columns: dict[str, Sequence[Any]]):
        if set(schema) != set(columns):
            raise ValueError("schema and columns must name the same columns")
        lengths = {len(col) for col in columns.values()}
        if len(lengths) > 1:
            raise ValueError("all columns must have the same length")
        self.schema: dict[str, ColType] = dict(schema)
        self.columns: dict[str, list[Any]] = {name: list(vals) for name, vals in columns.items()}
        self.nrows: int = lengths.pop() if lengths else 0
        for name, col_type in self.schema.items():
            for i, value in enumerate(self.columns[name]):
                if value is None:
                    continue  # NULL is allowed in any typed column
                if not _matches(col_type, value):
                    raise TypeError(
                        f"column {name!r} row {i}: {value!r} is not of type {col_type.value}"
                    )

    @classmethod
    def from_rows(cls, schema: dict[str, ColType], rows: Iterable[Sequence[Any]]) -> "Batch":
        names = list(schema)
        materialized = [tuple(row) for row in rows]
        columns = {name: [row[i] for row in materialized] for i, name in enumerate(names)}
        return cls(schema, columns)

    def iter_rows(self) -> Iterator[tuple[Any, ...]]:
        names = list(self.schema)
        cols = [self.columns[name] for name in names]
        for i in range(self.nrows):
            yield tuple(col[i] for col in cols)


# ---------------------------------------------------------------------------
# Sorted multiset backing the running minimum
# ---------------------------------------------------------------------------

def _sort_key(value: Any) -> tuple[int, Any]:
    # NaN sorts after every non-NaN value; everything else keeps natural order.
    if isinstance(value, float) and math.isnan(value):
        return (1, 0.0)
    return (0, value)


def _same_value(a: Any, b: Any) -> bool:
    if a is b:
        return True
    a_nan = isinstance(a, float) and math.isnan(a)
    b_nan = isinstance(b, float) and math.isnan(b)
    if a_nan or b_nan:
        return a_nan and b_nan
    return a == b


class _SortedMultiset:
    __slots__ = ("_keys", "_values")

    def __init__(self) -> None:
        self._keys: list[tuple[int, Any]] = []
        self._values: list[Any] = []

    def add(self, value: Any) -> None:
        key = _sort_key(value)
        i = bisect.bisect_left(self._keys, key)
        self._keys.insert(i, key)
        self._values.insert(i, value)

    def discard(self, value: Any) -> None:
        key = _sort_key(value)
        i = bisect.bisect_left(self._keys, key)
        while i < len(self._keys) and self._keys[i] == key:
            if _same_value(self._values[i], value):
                del self._keys[i]
                del self._values[i]
                return
            i += 1
        raise KeyError(f"value not present in multiset: {value!r}")

    def min(self) -> Any:
        return self._values[0] if self._values else None

    def __len__(self) -> int:
        return len(self._values)

    def __iter__(self) -> Iterator[Any]:
        return iter(self._values)


# ---------------------------------------------------------------------------
# Per-group state
# ---------------------------------------------------------------------------

class _GroupState:
    __slots__ = ("row_count", "sum", "_non_null", "_values", "_value_type")

    def __init__(self, value_type: ColType) -> None:
        self.row_count = 0          # count(*) -- counts rows, NULLs included
        self.sum: Any = None        # NULL until at least one non-null value
        self._non_null = 0
        self._values = _SortedMultiset()
        self._value_type = value_type

    def add(self, value: Any) -> None:
        self.row_count += 1
        if value is not None:
            self._non_null += 1
            self.sum = value if self.sum is None else self.sum + value
            self._values.add(value)

    def remove(self, value: Any) -> None:
        self.row_count -= 1
        if value is None:
            return
        self._non_null -= 1
        self._values.discard(value)
        if self._non_null == 0:
            self.sum = None
        elif self._can_subtract(value):
            self.sum = self.sum - value
        else:
            self.sum = self._recompute_sum()

    def _can_subtract(self, value: Any) -> bool:
        # Integer subtraction is exact, so it is always safe.  For floats it
        # is only safe while both sides are finite -- NaN/inf would otherwise
        # contaminate the running sum irreversibly (nan - nan == nan).
        if self._value_type is ColType.INT:
            return True
        if self._value_type is ColType.FLOAT:
            return math.isfinite(self.sum) and math.isfinite(value)
        return False  # e.g. strings: rebuild by folding over what remains

    def _recompute_sum(self) -> Any:
        if self._value_type is ColType.FLOAT:
            try:
                return math.fsum(self._values)
            except ValueError:  # inf and -inf mixed
                return sum(self._values)
        total: Any = None
        for v in self._values:
            total = v if total is None else total + v
        return total

    @property
    def min(self) -> Any:
        return self._values.min()


@dataclass(frozen=True)
class GroupRow:
    """One row of the aggregated result."""

    key: Any        # group key; None is the NULL-key group
    count: int      # count(*): number of rows in the group
    sum: Any        # sum of non-null values; None when every value is NULL
    min: Any        # smallest non-null value; None when every value is NULL


# ---------------------------------------------------------------------------
# The aggregate table
# ---------------------------------------------------------------------------

class GroupedAggregate:
    """Incrementally maintained count/sum/min per group key."""

    def __init__(self, key_type: ColType, value_type: ColType):
        self.key_type = key_type
        self.value_type = value_type
        self._rows: dict[Hashable, tuple[Any, Any]] = {}   # row_id -> (key, value)
        self._groups: dict[Any, _GroupState] = {}          # key -> state

    # -- introspection -----------------------------------------------------

    def __len__(self) -> int:
        return len(self._rows)

    def __contains__(self, row_id: Hashable) -> bool:
        return row_id in self._rows

    @property
    def group_count(self) -> int:
        return len(self._groups)

    def group(self, key: Any) -> GroupRow | None:
        state = self._groups.get(key)
        if state is None:
            return None
        return GroupRow(key=key, count=state.row_count, sum=state.sum, min=state.min)

    def result(self) -> list[GroupRow]:
        """All live groups, sorted by key with the NULL-key group last."""
        rows = [
            GroupRow(key=key, count=s.row_count, sum=s.sum, min=s.min)
            for key, s in self._groups.items()
        ]
        rows.sort(key=lambda r: (r.key is None, r.key))
        return rows

    # -- validation ---------------------------------------------------------

    def _check_key(self, key: Any) -> None:
        if key is None:
            return
        if not _matches(self.key_type, key):
            raise TypeError(f"group key {key!r} is not of type {self.key_type.value}")

    def _check_value(self, value: Any) -> None:
        if value is None:
            return
        if not _matches(self.value_type, value):
            raise TypeError(f"value {value!r} is not of type {self.value_type.value}")

    # -- public mutation API -------------------------------------------------

    def insert(self, row_id: Hashable, key: Any, value: Any) -> None:
        self.apply_ops([("insert", row_id, key, value)])

    def update(self, row_id: Hashable, *, key: Any = UNSET, value: Any = UNSET) -> None:
        """Change a row's key and/or value.  Pass ``None`` to set NULL.

        A key change migrates the row between groups atomically: the row is
        removed from its old group and added to the new one as a single
        validated operation, so it is never double counted.
        """
        self.apply_ops([("update", row_id, key, value)])

    def delete(self, row_id: Hashable) -> None:
        self.apply_ops([("delete", row_id)])

    def insert_batch(
        self,
        batch: Batch,
        *,
        id_col: str = "id",
        key_col: str = "key",
        value_col: str = "value",
    ) -> None:
        """Insert a whole typed batch atomically."""
        for name, col in ((id_col, "id"), (key_col, "key"), (value_col, "value")):
            if name not in batch.schema:
                raise ValueError(f"batch is missing the {col} column {name!r}")
        if batch.schema[key_col] is not self.key_type:
            raise TypeError(
                f"key column has type {batch.schema[key_col].value}, "
                f"expected {self.key_type.value}"
            )
        if batch.schema[value_col] is not self.value_type:
            raise TypeError(
                f"value column has type {batch.schema[value_col].value}, "
                f"expected {self.value_type.value}"
            )
        ops = [
            ("insert", rid, key, value)
            for rid, key, value in zip(
                batch.columns[id_col], batch.columns[key_col], batch.columns[value_col]
            )
        ]
        self.apply_ops(ops)

    def apply_ops(self, ops: Iterable[tuple]) -> None:
        """Validate a batch of operations, then apply them all.

        Validation runs entirely before mutation, so any error leaves the
        table exactly as it was (atomicity).
        """
        prepared: list[tuple] = []
        planned: dict[Hashable, tuple[Any, Any] | None] = {}  # pending view

        def exists(row_id: Hashable) -> bool:
            if row_id in planned:
                return planned[row_id] is not None
            return row_id in self._rows

        for op in ops:
            kind = op[0]
            if kind == "insert":
                _, row_id, key, value = op
                self._check_key(key)
                self._check_value(value)
                if exists(row_id):
                    raise KeyError(f"duplicate row id: {row_id!r}")
                planned[row_id] = (key, value)
                prepared.append(("insert", row_id, key, value))
            elif kind == "update":
                _, row_id, key, value = op
                if not exists(row_id):
                    raise KeyError(f"unknown row id: {row_id!r}")
                base = planned.get(row_id) or self._rows.get(row_id)
                new_key = base[0] if key is UNSET else key
                new_value = base[1] if value is UNSET else value
                self._check_key(new_key)
                self._check_value(new_value)
                planned[row_id] = (new_key, new_value)
                prepared.append(("update", row_id, new_key, new_value))
            elif kind == "delete":
                _, row_id = op
                if not exists(row_id):
                    raise KeyError(f"unknown row id: {row_id!r}")
                planned[row_id] = None
                prepared.append(("delete", row_id))
            else:
                raise ValueError(f"unknown operation: {kind!r}")

        for op in prepared:
            kind = op[0]
            if kind == "insert":
                _, row_id, key, value = op
                self._rows[row_id] = (key, value)
                self._group_add(key, value)
            elif kind == "update":
                _, row_id, key, value = op
                old_key, old_value = self._rows[row_id]
                self._rows[row_id] = (key, value)
                self._group_remove(old_key, old_value)
                self._group_add(key, value)
            else:  # delete
                _, row_id = op
                old_key, old_value = self._rows.pop(row_id)
                self._group_remove(old_key, old_value)

    # -- group bookkeeping ---------------------------------------------------

    def _group_add(self, key: Any, value: Any) -> None:
        state = self._groups.get(key)
        if state is None:
            state = self._groups[key] = _GroupState(self.value_type)
        state.add(value)

    def _group_remove(self, key: Any, value: Any) -> None:
        state = self._groups[key]
        state.remove(value)
        if state.row_count == 0:
            del self._groups[key]  # reclaim the empty group
