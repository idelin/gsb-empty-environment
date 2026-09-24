"""Incremental grouped aggregation (count / sum / min) over typed batches.

Pure standard library, Python 3.11+.

Semantics
---------
* Rows carry a unique row id; inserts, updates and deletes arrive in
  *typed batches* (:class:`Batch` declares a name+type per column).
* ``count`` is ``count(*)``: it counts rows, including rows whose value
  is null.  ``sum``/``min`` skip null values; a group whose values are
  all null reports ``sum=None`` and ``min=None`` while ``count`` still
  reflects the row count.
* ``None`` (null) and ``float('nan')`` are different things: null means
  "missing", NaN is a real float value.  NaN participates in ``sum``
  (making it NaN) but is ignored by ``min``.
* Integer columns are exact (Python arbitrary precision ints).
* Null keys form their own group; NaN keys are normalised to a single
  canonical NaN group.
* A batch is validated as a whole before any mutation, so a failing
  batch leaves the table untouched (atomic).  An update that moves a
  row from group A to group B removes it from A and adds it to B in the
  same commit, so it is never double counted.
* Groups whose row count drops to zero are reclaimed and disappear from
  the result.
"""

from __future__ import annotations

import heapq
import math
from collections import Counter
from dataclasses import dataclass
from typing import Any, Hashable, Iterable, Optional, Sequence

INT = "int"
FLOAT = "float"
STR = "str"
_COLUMN_TYPES = (INT, FLOAT, STR)

OP_INSERT = "I"
OP_UPDATE = "U"
OP_DELETE = "D"
_OPS = (OP_INSERT, OP_UPDATE, OP_DELETE)


class _NanKey:
    """Canonical group key for NaN keys (distinct from the null group)."""

    _instance: Optional["_NanKey"] = None

    def __new__(cls) -> "_NanKey":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NaN"


NAN_KEY = _NanKey()


def _is_nan(value: Any) -> bool:
    return isinstance(value, float) and math.isnan(value)


def _norm_key(key: Any) -> Hashable:
    return NAN_KEY if _is_nan(key) else key


def _check_value_type(value: Any, ctype: str, where: str) -> Any:
    """Validate one cell against its declared column type.

    Returns the value to store (ints are coerced to float in FLOAT
    columns so sums stay float).  ``None`` is always allowed (null).
    """
    if value is None:
        return None
    if ctype == INT:
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"{where}: expected int, got {type(value).__name__}")
        return value
    if ctype == FLOAT:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"{where}: expected float, got {type(value).__name__}")
        return float(value)
    if ctype == STR:
        if not isinstance(value, str):
            raise TypeError(f"{where}: expected str, got {type(value).__name__}")
        return value
    raise ValueError(f"unknown column type {ctype!r}")


@dataclass(frozen=True)
class Column:
    name: str
    type: str  # one of INT / FLOAT / STR

    def __post_init__(self) -> None:
        if self.type not in _COLUMN_TYPES:
            raise ValueError(f"unknown column type {self.type!r}")


class Batch:
    """A typed batch of rows: a schema plus row tuples."""

    def __init__(
        self,
        schema: Sequence,
        rows: Iterable,
    ) -> None:
        self.schema: tuple = tuple(
            c if isinstance(c, Column) else Column(*c) for c in schema
        )
        width = len(self.schema)
        self.rows: list = []
        for i, row in enumerate(rows):
            row = tuple(row)
            if len(row) != width:
                raise ValueError(
                    f"row {i}: expected {width} columns, got {len(row)}"
                )
            self.rows.append(
                tuple(
                    _check_value_type(v, col.type, f"row {i} column {col.name!r}")
                    for v, col in zip(row, self.schema)
                )
            )

    def column(self, name: str) -> int:
        for i, col in enumerate(self.schema):
            if col.name == name:
                return i
        raise KeyError(f"no column named {name!r}")

    def __len__(self) -> int:
        return len(self.rows)


class _GroupState:
    """Incremental aggregates for one group."""

    __slots__ = ("count", "non_null", "total", "nan_count", "_heap", "_removed")

    def __init__(self) -> None:
        self.count = 0          # count(*): rows in the group
        self.non_null = 0       # rows with a non-null value
        self.total = 0          # exact running sum of non-null, non-NaN values
        self.nan_count = 0      # non-null NaN values (kept out of `total`)
        self._heap: list = []         # min-heap of non-null, non-NaN values
        self._removed = Counter()     # lazy deletions from the heap

    def add(self, value: Any) -> None:
        self.count += 1
        if value is None:
            return
        self.non_null += 1
        if _is_nan(value):
            self.nan_count += 1
            return
        self.total += value
        heapq.heappush(self._heap, value)

    def remove(self, value: Any) -> None:
        self.count -= 1
        if value is None:
            return
        self.non_null -= 1
        if _is_nan(value):
            self.nan_count -= 1
            return
        self.total -= value
        self._removed[value] += 1

    def _purge_heap_top(self) -> None:
        removed = self._removed
        heap = self._heap
        while heap:
            top = heap[0]
            if removed.get(top, 0):
                heapq.heappop(heap)
                removed[top] -= 1
                if removed[top] == 0:
                    del removed[top]
            else:
                break

    @property
    def sum(self) -> Any:
        if self.non_null == 0:
            return None
        if self.nan_count:
            return float("nan")
        return self.total

    @property
    def min(self) -> Any:
        self._purge_heap_top()
        return self._heap[0] if self._heap else None


@dataclass(frozen=True)
class GroupResult:
    key: Any            # None for the null-key group, NAN_KEY for NaN keys
    count: int          # count(*): rows in the group
    non_null_count: int
    sum: Any            # None when every value in the group is null
    min: Any            # None when no non-null, non-NaN value exists


def _result_sort_key(result: GroupResult) -> tuple:
    key = result.key
    if key is None:
        return (3, 0, "")
    if key is NAN_KEY:
        return (2, 0, "")
    if isinstance(key, (int, float)):
        return (0, 0, key)
    return (1, 0, str(key))


class GroupedAggregate:
    """Maintains count/sum/min per key over a mutable set of rows."""

    def __init__(
        self,
        key_type: str = STR,
        value_type: str = FLOAT,
        *,
        id_type: str = INT,
        key_name: str = "key",
        value_name: str = "value",
        id_name: str = "id",
    ) -> None:
        for t in (key_type, value_type, id_type):
            if t not in _COLUMN_TYPES:
                raise ValueError(f"unknown column type {t!r}")
        self.key_type = key_type
        self.value_type = value_type
        self.id_type = id_type
        self.key_name = key_name
        self.value_name = value_name
        self.id_name = id_name
        self._rows: dict = {}     # id -> (key, value)
        self._groups: dict = {}   # normalised key -> _GroupState

    # ------------------------------------------------------------------ batches

    def _row_schema(self) -> list:
        return [
            Column(self.id_name, self.id_type),
            Column(self.key_name, self.key_type),
            Column(self.value_name, self.value_type),
        ]

    def _check_schema(self, batch: Batch, expected: list) -> None:
        if list(batch.schema) != expected:
            raise ValueError(
                f"batch schema {[(c.name, c.type) for c in batch.schema]} "
                f"does not match expected {[(c.name, c.type) for c in expected]}"
            )

    def insert_batch(self, batch: Batch) -> None:
        """Insert rows ``(id, key, value)``; ids must not already exist."""
        self._check_schema(batch, self._row_schema())
        self._apply([(OP_INSERT, rid, key, value) for rid, key, value in batch.rows])

    def update_batch(self, batch: Batch) -> None:
        """Update rows ``(id, key, value)``; ids must already exist.

        Moving a row to a different key migrates it between groups
        atomically within the batch commit.
        """
        self._check_schema(batch, self._row_schema())
        self._apply([(OP_UPDATE, rid, key, value) for rid, key, value in batch.rows])

    def delete_batch(self, batch: Batch) -> None:
        """Delete rows by id; batch has a single id column."""
        self._check_schema(batch, [Column(self.id_name, self.id_type)])
        self._apply([(OP_DELETE, rid, None, None) for (rid,) in batch.rows])

    def apply_batch(self, batch: Batch) -> None:
        """Apply a mixed batch with a leading ``op`` column of I/U/D."""
        expected = [Column("op", STR)] + self._row_schema()
        self._check_schema(batch, expected)
        ops = []
        for op, rid, key, value in batch.rows:
            if op not in _OPS:
                raise ValueError(f"unknown op {op!r}")
            if op == OP_DELETE:
                ops.append((OP_DELETE, rid, None, None))
            else:
                ops.append((op, rid, key, value))
        self._apply(ops)

    # ------------------------------------------------------------------ core

    def _validate(self, ops: list) -> None:
        """Validate the whole batch against current state (no mutation)."""
        staged: dict = {}  # id -> present after this batch so far
        for op, rid, _key, _value in ops:
            if not isinstance(rid, Hashable):
                raise TypeError(f"row id {rid!r} is not hashable")
            present = staged.get(rid, rid in self._rows)
            if op == OP_INSERT:
                if present:
                    raise KeyError(f"duplicate row id {rid!r}")
                staged[rid] = True
            elif op in (OP_UPDATE, OP_DELETE):
                if not present:
                    raise KeyError(f"unknown row id {rid!r}")
                staged[rid] = op == OP_UPDATE
            else:  # pragma: no cover - guarded by callers
                raise ValueError(f"unknown op {op!r}")

    def _apply(self, ops: list) -> None:
        # Phase 1: validate everything, so a bad batch changes nothing.
        self._validate(ops)
        # Phase 2: mutate.  An update removes from the old group and adds
        # to the new group inside this same commit, so cross-group moves
        # are atomic and never double counted.
        for op, rid, key, value in ops:
            if op == OP_INSERT:
                self._rows[rid] = (key, value)
                self._group_for(key).add(value)
            elif op == OP_UPDATE:
                old_key, old_value = self._rows[rid]
                self._remove_from_group(old_key, old_value)
                self._rows[rid] = (key, value)
                self._group_for(key).add(value)
            else:  # OP_DELETE
                old_key, old_value = self._rows.pop(rid)
                self._remove_from_group(old_key, old_value)

    def _group_for(self, key: Any) -> _GroupState:
        return self._groups.setdefault(_norm_key(key), _GroupState())

    def _remove_from_group(self, key: Any, value: Any) -> None:
        norm = _norm_key(key)
        group = self._groups[norm]
        group.remove(value)
        if group.count == 0:  # reclaim empty groups
            del self._groups[norm]

    # ------------------------------------------------------------------ reads

    def get(self, key: Any) -> Optional[GroupResult]:
        group = self._groups.get(_norm_key(key))
        if group is None:
            return None
        return GroupResult(
            key=NAN_KEY if _is_nan(key) else key,
            count=group.count,
            non_null_count=group.non_null,
            sum=group.sum,
            min=group.min,
        )

    def result(self) -> list:
        """Snapshot of all live groups, sorted (numbers, strings, NaN, null)."""
        out = [
            GroupResult(
                key=key,
                count=group.count,
                non_null_count=group.non_null,
                sum=group.sum,
                min=group.min,
            )
            for key, group in self._groups.items()
        ]
        out.sort(key=_result_sort_key)
        return out

    def __len__(self) -> int:
        return len(self._rows)

    def __contains__(self, key: Any) -> bool:
        return _norm_key(key) in self._groups
