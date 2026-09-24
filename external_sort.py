"""Budgeted external merge sort for typed record batches.

Pure standard library, Python 3.11+.

Features
--------
* Tables arrive as ``Batch`` objects carrying column types (schema).
* Configurable memory budget: buffered rows are sorted and spilled to
  temporary merge runs whenever the estimated buffer size reaches the
  budget.  A budget smaller than a single batch (even a single row)
  still works -- spilling simply happens more often.
* Mixed ascending/descending sort keys with explicit null placement
  (``nulls_first`` / ``nulls_last``), independent of direction.
* ``None`` (null) and ``float("nan")`` are distinct values: nulls are
  placed by the null option, NaN is a real float that sorts after every
  non-null number (ascending).
* Integers are compared exactly (Python arbitrary-precision ints).
* Stable: rows with equal sort keys keep their original input order,
  across spills and the k-way merge (a global sequence number breaks
  ties).
* Any failure (I/O, corrupt run, cancellation) cleans up temporary
  files and resets the sorter, so it can be reused immediately.
  Merge read errors propagate to the caller; partial output is never
  silently returned as success.
"""

from __future__ import annotations

import math
import os
import pickle
import shutil
import tempfile
import threading
from dataclasses import dataclass
from enum import Enum
from functools import cmp_to_key
from heapq import heappop, heappush
from typing import Iterator, Sequence

__all__ = [
    "Batch",
    "Column",
    "ColumnType",
    "ExternalSorter",
    "SortCancelled",
    "SortError",
    "SortKey",
    "estimate_row_size",
]

_RUN_MAGIC = "exsort-run-v1"
_ROW_OVERHEAD = 64


class SortError(Exception):
    """Base class for sorter failures."""


class SortCancelled(SortError):
    """Raised when a sort is cancelled via ``ExternalSorter.cancel()``."""


class ColumnType(Enum):
    INT = "int"
    FLOAT = "float"
    STRING = "string"
    BOOL = "bool"


@dataclass(frozen=True)
class Column:
    name: str
    type: ColumnType


@dataclass(frozen=True)
class SortKey:
    """One sort key: column (index or name), direction, null placement."""

    column: "int | str"
    ascending: bool = True
    nulls_first: bool = False


def _is_nan(value) -> bool:
    return isinstance(value, float) and math.isnan(value)


def _check_value_type(column: Column, value) -> None:
    if value is None:
        return
    ctype = column.type
    ok = (
        (ctype is ColumnType.INT and isinstance(value, int) and not isinstance(value, bool))
        or (
            ctype is ColumnType.FLOAT
            and isinstance(value, (int, float))
            and not isinstance(value, bool)
        )
        or (ctype is ColumnType.STRING and isinstance(value, str))
        or (ctype is ColumnType.BOOL and isinstance(value, bool))
    )
    if not ok:
        raise TypeError(
            f"column {column.name!r} expects {ctype.value}, "
            f"got {type(value).__name__}: {value!r}"
        )


class Batch:
    """A typed block of rows -- the unit of input to the sorter."""

    def __init__(self, columns: Sequence[Column], rows: Sequence[Sequence]):
        self.columns = tuple(columns)
        self.rows = []
        for row in rows:
            row = tuple(row)
            if len(row) != len(self.columns):
                raise ValueError(
                    f"row has {len(row)} values, "
                    f"schema has {len(self.columns)} columns"
                )
            for column, value in zip(self.columns, row):
                _check_value_type(column, value)
            self.rows.append(row)

    def __len__(self) -> int:
        return len(self.rows)


def _compare_scalar(a, b) -> int:
    """Total order for non-null values of one column.

    NaN sorts after every non-null number; NaN == NaN for ordering.
    Python compares int/float exactly, so big integers never lose
    precision against floats.
    """
    a_nan = _is_nan(a)
    b_nan = _is_nan(b)
    if a_nan or b_nan:
        if a_nan and b_nan:
            return 0
        return 1 if a_nan else -1
    if a < b:
        return -1
    if a > b:
        return 1
    return 0


def _compare_rows(a, b, keys) -> int:
    """Compare two rows by resolved keys: (index, ascending, nulls_first)."""
    for idx, ascending, nulls_first in keys:
        va = a[idx]
        vb = b[idx]
        a_null = va is None
        b_null = vb is None
        if a_null or b_null:
            if a_null and b_null:
                continue
            # Null placement is explicit and independent of ASC/DESC.
            if a_null:
                return -1 if nulls_first else 1
            return 1 if nulls_first else -1
        cmp = _compare_scalar(va, vb)
        if cmp:
            return cmp if ascending else -cmp
    return 0


def _estimate_value_size(value) -> int:
    if value is None:
        return 8
    if isinstance(value, bool):
        return 8
    if isinstance(value, int):
        return 32 + (abs(value).bit_length() + 7) // 8
    if isinstance(value, float):
        return 24
    if isinstance(value, str):
        return 49 + len(value)
    return 64


def estimate_row_size(row) -> int:
    """Rough in-memory footprint of one row, used for budget accounting."""
    return _ROW_OVERHEAD + sum(_estimate_value_size(v) for v in row)


class ExternalSorter:
    """Sort typed batches into a single ordered stream under a memory budget.

    Usage: create the sorter, feed it batches with ``add_batch``, then
    consume ``sort()`` (or ``sort_to_list()``).  After any failure or
    cancellation the sorter resets itself and may be reused.
    """

    def __init__(
        self,
        columns: Sequence[Column],
        sort_keys: Sequence[SortKey],
        mem_budget: int = 1 << 20,
        temp_dir: "str | None" = None,
    ):
        if mem_budget <= 0:
            raise ValueError("mem_budget must be positive")
        if not sort_keys:
            raise ValueError("at least one sort key is required")
        self.columns = tuple(columns)
        self._keys = tuple(self._resolve_key(k) for k in sort_keys)
        self.mem_budget = int(mem_budget)
        self.temp_dir = temp_dir or tempfile.gettempdir()
        self._row_key = cmp_to_key(lambda a, b: _compare_rows(a, b, self._keys))
        self._buffer: list = []
        self._buffer_bytes = 0
        self._seq = 0
        self._run_paths: list = []
        self._workdir: "str | None" = None
        self._cancel = threading.Event()

    # ------------------------------------------------------------------ API

    def add_batch(self, batch: Batch) -> None:
        """Buffer one batch, spilling a sorted run when the budget is hit."""
        try:
            self._check_cancel()
            self._check_schema(batch)
            for row in batch.rows:
                self._check_cancel()
                self._buffer.append((self._seq, row))
                self._seq += 1
                self._buffer_bytes += estimate_row_size(row)
                if self._buffer_bytes >= self.mem_budget:
                    self._spill()
        except BaseException:
            self._reset()
            raise

    def sort(self) -> Iterator[tuple]:
        """Return an iterator over all rows in sorted order.

        Errors (I/O, corrupt run, cancellation) propagate to the
        consumer; the iterator never ends silently early.  Temporary
        files are removed whether or not iteration completes.
        """
        return self._sort_impl()

    def sort_to_list(self) -> list:
        """Consume ``sort()`` fully; returns rows only on complete success."""
        return list(self.sort())

    def cancel(self) -> None:
        """Request cancellation; safe to call from another thread."""
        self._cancel.set()

    def reset(self) -> None:
        """Drop all buffered state and temp files; make the sorter reusable."""
        self._reset()

    # -------------------------------------------------------------- internal

    def _resolve_key(self, key: SortKey):
        col = key.column
        if isinstance(col, str):
            names = [c.name for c in self.columns]
            if col not in names:
                raise ValueError(f"unknown sort column {col!r}")
            col = names.index(col)
        if not 0 <= col < len(self.columns):
            raise ValueError(f"sort column index out of range: {col}")
        return (col, bool(key.ascending), bool(key.nulls_first))

    def _check_schema(self, batch: Batch) -> None:
        if batch.columns != self.columns:
            raise ValueError("batch schema does not match sorter schema")

    def _check_cancel(self) -> None:
        if self._cancel.is_set():
            raise SortCancelled("sort was cancelled")

    def _ensure_workdir(self) -> str:
        if self._workdir is None:
            self._workdir = tempfile.mkdtemp(prefix="exsort-", dir=self.temp_dir)
        return self._workdir

    def _spill(self) -> None:
        if not self._buffer:
            return
        self._buffer.sort(key=lambda rec: (self._row_key(rec[1]), rec[0]))
        fd, path = tempfile.mkstemp(
            prefix="run-", suffix=".bin", dir=self._ensure_workdir()
        )
        try:
            with os.fdopen(fd, "wb") as fh:
                # Header carries the record count so a truncated run is
                # detected instead of silently yielding partial output.
                pickle.dump((_RUN_MAGIC, len(self._buffer)), fh, protocol=4)
                for record in self._buffer:
                    pickle.dump(record, fh, protocol=4)
        except BaseException:
            _unlink_quietly(path)
            raise
        self._run_paths.append(path)
        self._buffer.clear()
        self._buffer_bytes = 0

    def _iter_run(self, path: str) -> Iterator:
        """Yield (seq, row) records from one run file; verify the trailer."""
        with open(path, "rb") as fh:
            try:
                magic, count = pickle.load(fh)
            except Exception as exc:
                raise SortError(f"corrupt run file {path!r}: {exc}") from exc
            if magic != _RUN_MAGIC:
                raise SortError(f"bad run file magic in {path!r}")
            for _ in range(count):
                try:
                    yield pickle.load(fh)
                except Exception as exc:
                    raise SortError(f"corrupt or truncated run file "
                                    f"{path!r}: {exc}") from exc

    def _merge_records(self) -> Iterator[tuple]:
        runs = [self._iter_run(path) for path in self._run_paths]
        try:
            heap = []
            for it in runs:
                try:
                    seq, row = next(it)
                except StopIteration:
                    continue
                heappush(heap, (self._row_key(row), seq, row, it))
            while heap:
                self._check_cancel()
                _, _, row, it = heappop(heap)
                yield row
                try:
                    seq, row = next(it)
                except StopIteration:
                    continue
                # seq is unique, so the iterator is never compared.
                heappush(heap, (self._row_key(row), seq, row, it))
        finally:
            for it in runs:
                it.close()

    def _sort_impl(self) -> Iterator[tuple]:
        try:
            self._check_cancel()
            if not self._run_paths:
                # Everything fits in memory: no temp files at all.
                self._buffer.sort(key=lambda rec: (self._row_key(rec[1]), rec[0]))
                for _, row in self._buffer:
                    self._check_cancel()
                    yield row
            else:
                self._spill()
                yield from self._merge_records()
        finally:
            self._reset()

    def _reset(self) -> None:
        self._buffer.clear()
        self._buffer_bytes = 0
        self._seq = 0
        self._cancel.clear()
        self._run_paths.clear()
        if self._workdir is not None:
            shutil.rmtree(self._workdir, ignore_errors=True)
            self._workdir = None


def _unlink_quietly(path: str) -> None:
    try:
        os.unlink(path)
    except OSError:
        pass
