"""External merge sort for typed record batches (Python 3.11, stdlib only).

Rows arrive as :class:`Batch` objects described by a :class:`Schema`.
Rows are buffered up to a configurable memory budget (in rows); whenever the
budget is exceeded the buffer is sorted and spilled to a temporary run file.
``sort()`` performs a k-way merge over the runs and returns sorted batches.

Properties:

* Mixed ascending/descending sort keys with explicit null placement
  (``nulls_first`` / ``nulls_last``), independent of sort direction.
* Stable: rows with equal sort keys keep their original input order.
* ``None`` (null) is distinct from ``NaN``; NaN sorts after all non-NaN
  numbers in a float column.
* Integers are compared exactly (never converted to float).
* A budget smaller than a single batch still works: large batches are
  chunked and spilled piece by piece.
* On any failure (including injected merge read errors) temporary files are
  removed and no partial/fake result is produced: ``sort()`` either returns
  the complete result or raises.
* ``cancel()`` aborts the running operation; the sorter resets itself and
  can be used again afterwards.
"""

from __future__ import annotations

import heapq
import math
import os
import pickle
import shutil
import tempfile
from dataclasses import dataclass
from functools import cmp_to_key
from typing import Callable, Iterator, Optional, Sequence

__all__ = [
    "Batch",
    "Column",
    "ExternalSorter",
    "Schema",
    "SortCancelled",
    "SortKey",
]

_COLUMN_TYPES = frozenset({"int", "float", "str", "bool"})

_END_OF_RUN = None  # sentinel pickled at the end of each run file


class SortCancelled(Exception):
    """Raised when the current operation is aborted via ``cancel()``."""


@dataclass(frozen=True)
class Column:
    """A named, typed column. ``type`` is one of int/float/str/bool."""

    name: str
    type: str

    def __post_init__(self) -> None:
        if self.type not in _COLUMN_TYPES:
            raise ValueError(f"unsupported column type: {self.type!r}")


@dataclass(frozen=True)
class SortKey:
    """One sort key: column (name or index), direction, null placement."""

    column: object  # column name (str) or 0-based index (int)
    ascending: bool = True
    nulls_first: bool = False


class Schema:
    """Ordered collection of :class:`Column`."""

    def __init__(self, columns: Sequence[Column | tuple]) -> None:
        cols = tuple(c if isinstance(c, Column) else Column(*c) for c in columns)
        if not cols:
            raise ValueError("schema must contain at least one column")
        names = [c.name for c in cols]
        if len(set(names)) != len(names):
            raise ValueError(f"duplicate column names: {names!r}")
        self._columns = cols
        self._index = {c.name: i for i, c in enumerate(cols)}

    @property
    def columns(self) -> tuple:
        return self._columns

    def index(self, ref) -> int:
        if isinstance(ref, int) and not isinstance(ref, bool):
            if 0 <= ref < len(self._columns):
                return ref
            raise KeyError(f"column index out of range: {ref}")
        try:
            return self._index[ref]
        except KeyError:
            raise KeyError(f"unknown column: {ref!r}") from None

    def __len__(self) -> int:
        return len(self._columns)

    def __eq__(self, other) -> bool:
        return isinstance(other, Schema) and self._columns == other._columns

    def __hash__(self) -> int:
        return hash(self._columns)

    def __repr__(self) -> str:
        return f"Schema({list(self._columns)!r})"


def _check_value(value, column: Column) -> None:
    if value is None:  # every column is nullable; null is not NaN
        return
    ctype = column.type
    if ctype == "int":
        ok = isinstance(value, int) and not isinstance(value, bool)
    elif ctype == "float":
        ok = isinstance(value, (int, float)) and not isinstance(value, bool)
    elif ctype == "str":
        ok = isinstance(value, str)
    else:  # bool
        ok = isinstance(value, bool)
    if not ok:
        raise TypeError(
            f"column {column.name!r} ({ctype}): invalid value {value!r}"
        )


class Batch:
    """A set of rows conforming to a :class:`Schema`."""

    def __init__(self, schema: Schema, rows: Sequence[Sequence]) -> None:
        self.schema = schema
        self.rows = []
        width = len(schema)
        for row in rows:
            row = tuple(row)
            if len(row) != width:
                raise ValueError(
                    f"row has {len(row)} values, schema has {width} columns: {row!r}"
                )
            for value, column in zip(row, schema.columns):
                _check_value(value, column)
            self.rows.append(row)

    def __len__(self) -> int:
        return len(self.rows)

    def __repr__(self) -> str:
        return f"Batch(rows={self.rows!r})"


def _compare_non_null(a, b) -> int:
    """Compare two non-null values of the same column.

    NaN is a regular (non-null) value and sorts after every non-NaN number.
    Integers compare exactly; mixed int/float comparison is exact in Python.
    """
    a_nan = isinstance(a, float) and math.isnan(a)
    b_nan = isinstance(b, float) and math.isnan(b)
    if a_nan or b_nan:
        if a_nan and b_nan:
            return 0
        return 1 if a_nan else -1
    if a < b:
        return -1
    if a > b:
        return 1
    return 0


class _RunWriter:
    """Writes sorted entries to a run file, terminated by a sentinel."""

    def __init__(self, path: str) -> None:
        self._fh = open(path, "wb")

    def write(self, entry) -> None:
        pickle.dump(entry, self._fh, protocol=pickle.HIGHEST_PROTOCOL)

    def close(self) -> None:
        pickle.dump(_END_OF_RUN, self._fh, protocol=pickle.HIGHEST_PROTOCOL)
        self._fh.close()


class _RunReader:
    """Reads entries back from a run file.

    A truncated file (EOF before the end sentinel) raises ``IOError``
    instead of silently pretending the run ended, so a merge read error can
    never produce a partial result that looks successful.
    """

    def __init__(self, path: str) -> None:
        self._fh = open(path, "rb")

    def next(self):
        try:
            obj = pickle.load(self._fh)
        except EOFError as exc:
            raise IOError("run file truncated: missing end-of-run sentinel") from exc
        return None if obj is _END_OF_RUN else obj

    def close(self) -> None:
        self._fh.close()


class ExternalSorter:
    """Sorts batches into temporary runs and merges them within a budget."""

    def __init__(
        self,
        schema: Schema,
        sort_keys: Sequence[SortKey],
        max_rows_in_memory: int = 1024,
        temp_dir: Optional[str] = None,
        output_batch_size: Optional[int] = None,
        reader_factory: Optional[Callable[[str], object]] = None,
    ) -> None:
        if max_rows_in_memory < 1:
            raise ValueError("max_rows_in_memory must be >= 1")
        self._schema = schema
        self._keys = [
            (schema.index(k.column), k.ascending, k.nulls_first) for k in sort_keys
        ]
        self._budget = max_rows_in_memory
        self._temp_dir = temp_dir
        self._output_batch_size = output_batch_size or max_rows_in_memory
        # Test hook: allows injecting failing/cancelling run readers.
        self._reader_factory = reader_factory or _RunReader
        self._reset_state()

    # -- state management -------------------------------------------------

    def _reset_state(self) -> None:
        self._buffer: list = []  # list of (seq, row)
        self._seq = 0
        self._cancelled = False
        self._run_paths: list = []
        if getattr(self, "_work_dir", None):
            shutil.rmtree(self._work_dir, ignore_errors=True)
        self._work_dir = None

    @property
    def run_file_paths(self) -> tuple:
        """Paths of the currently spilled run files (for inspection/tests)."""
        return tuple(self._run_paths)

    def cancel(self) -> None:
        """Abort the running operation; the sorter resets and stays usable."""
        self._cancelled = True

    def _check_cancelled(self) -> None:
        if self._cancelled:
            raise SortCancelled("operation cancelled")

    # -- comparator ---------------------------------------------------------

    def _compare(self, entry_a, entry_b) -> int:
        row_a, row_b = entry_a[1], entry_b[1]
        for idx, ascending, nulls_first in self._keys:
            a, b = row_a[idx], row_b[idx]
            if a is None or b is None:
                if a is None and b is None:
                    continue
                a_is_null = a is None
                # Null placement is explicit and NOT flipped by descending.
                if nulls_first:
                    return -1 if a_is_null else 1
                else:
                    return 1 if a_is_null else -1
            else:
                cmp = _compare_non_null(a, b)
                if cmp:
                    return cmp if ascending else -cmp
        # Stability: equal keys keep original input order.
        seq_a, seq_b = entry_a[0], entry_b[0]
        return (seq_a > seq_b) - (seq_a < seq_b)

    # -- ingest / spill -----------------------------------------------------

    def add_batch(self, batch: Batch) -> None:
        if not isinstance(batch, Batch):
            raise TypeError(f"expected Batch, got {type(batch).__name__}")
        if batch.schema != self._schema:
            raise ValueError("batch schema does not match sorter schema")
        try:
            for row in batch.rows:
                self._check_cancelled()
                self._buffer.append((self._seq, row))
                self._seq += 1
                if len(self._buffer) >= self._budget:
                    self._spill()
        except SortCancelled:
            self._reset_state()
            raise

    def _ensure_work_dir(self) -> None:
        if self._work_dir is None:
            self._work_dir = tempfile.mkdtemp(
                prefix="extsort-", dir=self._temp_dir
            )

    def _spill(self) -> None:
        if not self._buffer:
            return
        self._ensure_work_dir()
        self._buffer.sort(key=cmp_to_key(self._compare))
        path = os.path.join(self._work_dir, f"run-{len(self._run_paths):06d}.bin")
        writer = _RunWriter(path)
        try:
            for entry in self._buffer:
                writer.write(entry)
        finally:
            writer.close()
        self._run_paths.append(path)
        self._buffer.clear()

    # -- merge / output -----------------------------------------------------

    def _merge(self) -> list:
        readers = []
        heap = []
        key_fn = cmp_to_key(self._compare)
        try:
            for path in self._run_paths:
                readers.append(self._reader_factory(path))
            for idx, reader in enumerate(readers):
                entry = reader.next()
                if entry is not None:
                    heapq.heappush(heap, (key_fn(entry), idx, entry))
            merged = []
            while heap:
                self._check_cancelled()
                _, idx, entry = heapq.heappop(heap)
                merged.append(entry)
                nxt = readers[idx].next()
                if nxt is not None:
                    heapq.heappush(heap, (key_fn(nxt), idx, nxt))
            return merged
        finally:
            for reader in readers:
                close = getattr(reader, "close", None)
                if close is not None:
                    try:
                        close()
                    except Exception:
                        pass

    def _to_batches(self, entries) -> list:
        rows = [row for _, row in entries]
        size = self._output_batch_size
        return [
            Batch(self._schema, rows[i : i + size])
            for i in range(0, len(rows), size)
        ]

    def sort(self) -> list:
        """Sort all added batches and return a list of sorted batches.

        Either the complete, correctly sorted result is returned, or an
        exception is raised; a failure never yields partial output and
        always removes temporary files. The sorter is reset afterwards and
        can be reused immediately.
        """
        try:
            self._check_cancelled()
            self._spill()
            if self._run_paths:
                entries = self._merge()
            else:
                self._buffer.sort(key=cmp_to_key(self._compare))
                entries = self._buffer
            return self._to_batches(entries)
        finally:
            self._reset_state()


def _demo() -> None:
    schema = Schema([("id", "int"), ("score", "float"), ("name", "str")])
    keys = [
        SortKey("score", ascending=False, nulls_first=False),
        SortKey("id", ascending=True),
    ]
    nan = float("nan")
    batches = [
        Batch(schema, [(3, 1.5, "c"), (1, None, "a"), (2, nan, "b")]),
        Batch(schema, [(5, -2.0, "e"), (4, 1.5, "d")]),
        Batch(schema, [(7, None, "g"), (6, 10.0, "f"), (8, 1.5, "h")]),
    ]
    print("input batches:")
    for batch in batches:
        for row in batch.rows:
            print(f"  {row}")

    sorter = ExternalSorter(schema, keys, max_rows_in_memory=4)
    for batch in batches:
        sorter.add_batch(batch)
    print(f"\nspilled run files: {len(sorter.run_file_paths)} "
          f"(memory budget: 4 rows)")
    result = sorter.sort()

    print("\nsorted output (score desc/nulls last, then id asc; NaN is not null):")
    for batch in result:
        for row in batch.rows:
            print(f"  {row}")


if __name__ == "__main__":
    _demo()
