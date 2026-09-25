"""Partitioned (GRACE-style) equi-join with disk spill.

Supported join types: INNER, LEFT, FULL OUTER.

Semantics
---------
* Equi-join on one or more key columns of matching type.
* Duplicate keys expand with Cartesian multiplicity: a key appearing ``m``
  times on the left and ``n`` times on the right yields ``m * n`` rows.
* ``NULL`` keys never match anything (not even ``NULL``); outer joins keep
  them as unmatched rows per SQL semantics.
* ``NaN`` is a normal FLOAT value, distinct from ``NULL``; ``NaN`` matches
  ``NaN`` (normalized to a single token before hashing/comparison).
* Integers are exact arbitrary-precision Python ints.

Execution model
---------------
1. Both inputs are streamed batch-by-batch and hash-partitioned into
   ``num_partitions`` partitions. Partition buffers spill to pickle files
   under a temp directory once they exceed a flush threshold derived from
   ``memory_limit_rows``. Rows whose key contains NULL go to a dedicated
   side partition (they can never match).
2. Each pair of same-index partitions is joined independently. If a pair
   does not fit in ``memory_limit_rows`` it is recursively re-partitioned
   with a depth-salted hash. Recursion stops when (a) the pair fits,
   (b) ``max_depth`` is reached, or (c) re-partitioning makes no progress
   (e.g. all keys identical) -- in cases (b)/(c) the pair is joined in
   memory as a fallback, so pathological data can never cause infinite
   recursion.
3. Output order is definable via ``output_order``:
   ``OutputOrder.LEFT`` (default) emits matched/unmatched-left rows in
   left input order (matches of one left row in right input order), then
   -- for FULL joins -- unmatched right rows in right input order.
   ``OutputOrder.PARTITION`` emits rows in partition processing order
   (cheaper, but only deterministic for a fixed hash seed).
"""

from __future__ import annotations

import enum
import math
import os
import pickle
import tempfile
from typing import Any, Iterable, Iterator, Sequence

from .types import NULL, Batch, Column, Schema


class JoinType(enum.Enum):
    INNER = "inner"
    LEFT = "left"
    FULL = "full"


class OutputOrder(enum.Enum):
    LEFT = "left"
    PARTITION = "partition"


class _NeverMatch:
    """Key sentinel for rows that can never match (key contains NULL)."""

    _instance: "_NeverMatch | None" = None

    def __new__(cls) -> "_NeverMatch":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NEVER_MATCH"


NEVER_MATCH = _NeverMatch()

_NAN_TOKEN = ("float", "<NaN>")


def _normalize(value: Any) -> tuple[str, Any]:
    """Map a key value to a hashable, type-tagged, exactly-comparable token."""
    if isinstance(value, bool):
        return ("bool", value)
    if isinstance(value, int):
        return ("int", value)  # exact, arbitrary precision
    if isinstance(value, float):
        if math.isnan(value):
            return _NAN_TOKEN
        return ("float", value)
    return ("str", value)


def _build_key(row: tuple[Any, ...], key_cols: Sequence[int]) -> Any:
    parts = []
    for col in key_cols:
        value = row[col]
        if value is NULL:
            return NEVER_MATCH
        parts.append(_normalize(value))
    return tuple(parts)


# An entry is (global_row_index, key, row).
_Entry = tuple[int, Any, tuple[Any, ...]]


class _SpillPartition:
    """A partition buffer that spills pickled entry lists to disk."""

    def __init__(self, directory: str, name: str, flush_threshold: int):
        self._dir = directory
        self._name = name
        self._flush_threshold = max(1, flush_threshold)
        self._buffer: list[_Entry] = []
        self._files: list[str] = []
        self.count = 0

    def add(self, entry: _Entry) -> None:
        self._buffer.append(entry)
        self.count += 1
        if len(self._buffer) >= self._flush_threshold:
            self.flush()

    def flush(self) -> None:
        if not self._buffer:
            return
        path = os.path.join(self._dir, f"{self._name}-{len(self._files)}.spill")
        with open(path, "wb") as fh:
            pickle.dump(self._buffer, fh, protocol=pickle.HIGHEST_PROTOCOL)
        self._files.append(path)
        self._buffer = []

    def iter_entries(self) -> Iterator[_Entry]:
        for path in self._files:
            with open(path, "rb") as fh:
                yield from pickle.load(fh)
        yield from self._buffer

    def load(self) -> list[_Entry]:
        return list(self.iter_entries())


class Joiner:
    """Streams two typed tables through a partitioned equi-join."""

    def __init__(
        self,
        left_schema: Schema,
        right_schema: Schema,
        left_keys: Sequence[str | int],
        right_keys: Sequence[str | int],
        join_type: JoinType = JoinType.INNER,
        memory_limit_rows: int = 10_000,
        num_partitions: int = 16,
        max_depth: int = 8,
        output_order: OutputOrder = OutputOrder.LEFT,
        output_batch_size: int = 1_000,
        tmp_dir: str | None = None,
    ):
        if len(left_keys) != len(right_keys) or not left_keys:
            raise ValueError("left_keys/right_keys must be non-empty and equal length")
        if memory_limit_rows < 1:
            raise ValueError("memory_limit_rows must be >= 1")
        if num_partitions < 2:
            raise ValueError("num_partitions must be >= 2")
        self.left_schema = left_schema
        self.right_schema = right_schema
        self._left_key_cols = [left_schema.column_index(k) for k in left_keys]
        self._right_key_cols = [right_schema.column_index(k) for k in right_keys]
        for lc, rc in zip(self._left_key_cols, self._right_key_cols):
            lt = left_schema.columns[lc].col_type
            rt = right_schema.columns[rc].col_type
            if lt is not rt:
                raise TypeError(
                    f"key type mismatch: {left_schema.columns[lc].name} is "
                    f"{lt.value} but {right_schema.columns[rc].name} is {rt.value}"
                )
        self.join_type = join_type
        self._memory_limit_rows = memory_limit_rows
        self._num_partitions = num_partitions
        self._max_depth = max_depth
        self._output_order = output_order
        self._output_batch_size = max(1, output_batch_size)
        self._tmp_dir = tmp_dir
        # Total buffered rows across both inputs stay under the limit.
        self._flush_threshold = max(1, memory_limit_rows // (2 * num_partitions))
        self._null_left = tuple(NULL for _ in left_schema.columns)
        self._null_right = tuple(NULL for _ in right_schema.columns)
        self._output_schema = self._build_output_schema()

    @property
    def output_schema(self) -> Schema:
        return self._output_schema

    def _build_output_schema(self) -> Schema:
        columns = list(self.left_schema.columns)
        used = {c.name for c in columns}
        for col in self.right_schema.columns:
            name = col.name
            while name in used:
                name = "right." + name
            used.add(name)
            columns.append(Column(name, col.col_type))
        return Schema(columns)

    # ------------------------------------------------------------------ API

    def execute(
        self,
        left_batches: Iterable[Batch],
        right_batches: Iterable[Batch],
    ) -> list[Batch]:
        """Run the join and return the result as a list of output batches."""
        with tempfile.TemporaryDirectory(dir=self._tmp_dir) as tmp:
            left_parts, left_null = self._partition_input(
                left_batches, self.left_schema, self._left_key_cols, tmp, "L"
            )
            right_parts, right_null = self._partition_input(
                right_batches, self.right_schema, self._right_key_cols, tmp, "R"
            )
            results: list[tuple[tuple[int, int, int], tuple[Any, ...]]] = []
            for p in range(self._num_partitions):
                self._join_partition(left_parts[p], right_parts[p], tmp, 0, results)
            self._emit_null_side(left_null, is_left=True, out=results)
            self._emit_null_side(right_null, is_left=False, out=results)

        if self._output_order is OutputOrder.LEFT:
            results.sort(key=lambda item: item[0])
        rows = [row for _, row in results]
        return [
            Batch(self._output_schema, rows[i : i + self._output_batch_size])
            for i in range(0, len(rows), self._output_batch_size)
        ]

    def collect(
        self,
        left_batches: Iterable[Batch],
        right_batches: Iterable[Batch],
    ) -> list[tuple[Any, ...]]:
        """Convenience: run the join and return a flat list of rows."""
        return [row for batch in self.execute(left_batches, right_batches) for row in batch]

    # ------------------------------------------------------- partitioning

    def _partition_input(
        self,
        batches: Iterable[Batch],
        schema: Schema,
        key_cols: Sequence[int],
        tmp: str,
        prefix: str,
    ) -> tuple[list[_SpillPartition], _SpillPartition]:
        parts = [
            _SpillPartition(tmp, f"{prefix}p{p}", self._flush_threshold)
            for p in range(self._num_partitions)
        ]
        null_part = _SpillPartition(tmp, f"{prefix}null", self._flush_threshold)
        index = 0
        for batch in batches:
            if batch.schema != schema:
                raise ValueError(
                    f"batch schema {batch.schema!r} does not match {schema!r}"
                )
            for row in batch:
                key = _build_key(row, key_cols)
                if key is NEVER_MATCH:
                    null_part.add((index, key, row))
                else:
                    parts[hash(key) % self._num_partitions].add((index, key, row))
                index += 1
        return parts, null_part

    def _repartition(
        self, part: _SpillPartition, tmp: str, name: str, depth: int
    ) -> list[_SpillPartition]:
        subs = [
            _SpillPartition(tmp, f"{name}s{i}", self._flush_threshold)
            for i in range(self._num_partitions)
        ]
        for entry in part.iter_entries():
            subs[hash((depth, entry[1])) % self._num_partitions].add(entry)
        return subs

    # ------------------------------------------------------------ joining

    def _join_partition(
        self,
        left_part: _SpillPartition,
        right_part: _SpillPartition,
        tmp: str,
        depth: int,
        out: list,
    ) -> None:
        total = left_part.count + right_part.count
        if total == 0:
            return
        if total <= self._memory_limit_rows or depth >= self._max_depth:
            self._in_memory_join(left_part.load(), right_part.load(), out)
            return
        sub_left = self._repartition(left_part, tmp, f"d{depth}L{id(left_part)}", depth)
        sub_right = self._repartition(right_part, tmp, f"d{depth}R{id(right_part)}", depth)
        left_shrank = max(p.count for p in sub_left) < left_part.count
        right_shrank = max(p.count for p in sub_right) < right_part.count
        if not left_shrank and not right_shrank:
            # No progress (e.g. all keys identical): never recurse forever.
            self._in_memory_join(left_part.load(), right_part.load(), out)
            return
        for sl, sr in zip(sub_left, sub_right):
            self._join_partition(sl, sr, tmp, depth + 1, out)

    def _in_memory_join(
        self,
        left_entries: list[_Entry],
        right_entries: list[_Entry],
        out: list,
    ) -> None:
        build: dict[Any, list[tuple[int, tuple[Any, ...]]]] = {}
        for ridx, rkey, rrow in right_entries:
            build.setdefault(rkey, []).append((ridx, rrow))
        matched_right: set[int] = set()
        for lidx, lkey, lrow in left_entries:
            matches = build.get(lkey)
            if matches:
                for ridx, rrow in matches:
                    matched_right.add(ridx)
                    out.append(((0, lidx, ridx), lrow + rrow))
            elif self.join_type in (JoinType.LEFT, JoinType.FULL):
                out.append(((0, lidx, -1), lrow + self._null_right))
        if self.join_type is JoinType.FULL:
            for ridx, _rkey, rrow in right_entries:
                if ridx not in matched_right:
                    out.append(((1, ridx, 0), self._null_left + rrow))

    def _emit_null_side(
        self, null_part: _SpillPartition, is_left: bool, out: list
    ) -> None:
        """NULL-keyed rows never match; outer joins still keep them."""
        if null_part.count == 0:
            return
        if is_left and self.join_type in (JoinType.LEFT, JoinType.FULL):
            for lidx, _key, row in null_part.iter_entries():
                out.append(((0, lidx, -1), row + self._null_right))
        elif not is_left and self.join_type is JoinType.FULL:
            for ridx, _key, row in null_part.iter_entries():
                out.append(((1, ridx, 0), self._null_left + row))
