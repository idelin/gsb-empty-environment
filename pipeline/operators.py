"""Concrete pull operators with bounded batches.

NULL / three-valued logic
-------------------------
``None`` models SQL NULL.  Comparison helpers (:func:`sql_eq`, :func:`sql_lt`,
...) return ``None`` (UNKNOWN) when either side is NULL.  ``Filter`` keeps a
row only when its predicate returns true; UNKNOWN and False are both
rejected.  Join keys containing NULL never match (NULL != NULL).

Multiset (bag) semantics
------------------------
Duplicate rows are preserved by Scan/Filter/Project.  ``HashJoin`` produces
the bag product of matches: if a key occurs ``m`` times on the left and
``n`` times on the right, the join emits ``m * n`` rows.
"""

from __future__ import annotations

import csv
from collections import deque
from typing import Callable, Optional, Sequence

from .core import Batch, CancelToken, Operator, Row, UnaryOperator

# --------------------------------------------------------------------------
# NULL-aware (three-valued) comparisons: return True / False / None(UNKNOWN)
# --------------------------------------------------------------------------

def _unknown_if_null(a, b):
    return None if a is None or b is None else "cmp"


def sql_eq(a, b):
    return None if a is None or b is None else a == b


def sql_ne(a, b):
    return None if a is None or b is None else a != b


def sql_lt(a, b):
    return None if a is None or b is None else a < b


def sql_le(a, b):
    return None if a is None or b is None else a <= b


def sql_gt(a, b):
    return None if a is None or b is None else a > b


def sql_ge(a, b):
    return None if a is None or b is None else a >= b


# --------------------------------------------------------------------------
# Scan: stream a CSV file in bounded batches (never loads the whole file)
# --------------------------------------------------------------------------

class CsvScan(Operator):
    """Pull rows from a CSV file, at most ``max_rows`` per batch.

    ``null_token`` (default empty string) maps to ``None``.  ``types`` is an
    optional per-column converter sequence applied to non-NULL fields.
    ``rows_read`` exposes how many data rows were actually consumed, so
    callers can verify that e.g. LIMIT 1 does not scan the whole file.
    """

    def __init__(
        self,
        path: str,
        types: Optional[Sequence[type]] = None,
        null_token: str = "",
        has_header: bool = True,
        token: Optional[CancelToken] = None,
    ) -> None:
        super().__init__(token)
        self.path = path
        self.types = types
        self.null_token = null_token
        self.has_header = has_header
        self.rows_read = 0
        self._fh = None
        self._reader = None

    def _on_open(self) -> None:
        self._fh = open(self.path, "r", newline="", encoding="utf-8")
        self._reader = csv.reader(self._fh)
        if self.has_header:
            next(self._reader, None)

    def _on_close(self) -> None:
        if self._fh is not None:
            self._fh.close()

    @property
    def file_closed(self) -> bool:
        return self._fh is None or self._fh.closed

    def _convert(self, raw: list) -> Row:
        out = []
        for i, field in enumerate(raw):
            if field == self.null_token:
                out.append(None)
            elif self.types is not None and i < len(self.types) and self.types[i] is not str:
                out.append(self.types[i](field))
            else:
                out.append(field)
        return tuple(out)

    def next_batch(self, max_rows: int) -> Optional[Batch]:
        self._check_pullable()
        batch: Batch = []
        for raw in self._reader:
            batch.append(self._convert(raw))
            if len(batch) >= max_rows:
                break
        self.rows_read += len(batch)
        if not batch:
            return None  # source exhausted -> EOF (never an empty batch)
        return batch


# --------------------------------------------------------------------------
# Filter / Project
# --------------------------------------------------------------------------

class Filter(UnaryOperator):
    """Keep rows for which ``predicate(row)`` is true; UNKNOWN/False drop."""

    def __init__(self, child: Operator, predicate: Callable[[Row], object]) -> None:
        super().__init__(child)
        self.predicate = predicate

    def next_batch(self, max_rows: int) -> Optional[Batch]:
        self._check_pullable()
        out: Batch = []
        while len(out) < max_rows:
            rows = self._pull_rows(max_rows - len(out))
            if rows is None:
                return out if out else None
            if not rows:
                return out  # child signalled "no rows right now"
            for row in rows:
                if self.predicate(row):  # None (UNKNOWN) is falsy -> dropped
                    out.append(row)
        return out


class Project(UnaryOperator):
    """Map each row through ``selector``; duplicates are preserved."""

    def __init__(self, child: Operator, selector: Callable[[Row], Row]) -> None:
        super().__init__(child)
        self.selector = selector

    def next_batch(self, max_rows: int) -> Optional[Batch]:
        self._check_pullable()
        rows = self._pull_rows(max_rows)
        if rows is None:
            return None
        return [self.selector(row) for row in rows]


# --------------------------------------------------------------------------
# Limit: stops early and releases upstream as soon as the limit is reached
# --------------------------------------------------------------------------

class Limit(UnaryOperator):
    def __init__(self, child: Operator, n: int) -> None:
        if n < 0:
            raise ValueError("limit must be >= 0")
        super().__init__(child)
        self.remaining = n

    def next_batch(self, max_rows: int) -> Optional[Batch]:
        self._check_pullable()
        if self.remaining == 0:
            return None
        rows = self._pull_rows(min(max_rows, self.remaining))
        if rows is None:
            return None
        self.remaining -= len(rows)
        if self.remaining == 0:
            self.child.close()  # early stop: release the whole upstream now
        return rows


# --------------------------------------------------------------------------
# HashJoin: materializes the right (build) side, streams the left (probe)
# --------------------------------------------------------------------------

class HashJoin(Operator):
    """Equi-join on key functions; NULL keys never match; bag semantics."""

    def __init__(self, left: Operator, right: Operator,
                 left_key: Callable[[Row], tuple],
                 right_key: Callable[[Row], tuple]) -> None:
        super().__init__(left.token)
        self.left = left
        self.right = right
        self.left_key = left_key
        self.right_key = right_key
        self._table: dict = {}
        self._pending: deque = deque()
        self._left_eof = False

    def _on_open(self) -> None:
        self.left.open()
        self.right.open()
        try:
            while True:
                batch = self.right.next_batch(4096)
                if batch is None:
                    break
                for row in batch:
                    key = self.right_key(row)
                    if key is not None and all(v is not None for v in key):
                        self._table.setdefault(key, []).append(row)
        except BaseException:
            self.close()
            raise
        finally:
            self.right.close()  # build side fully consumed -> release it

    def _close_children(self) -> None:
        self.left.close()
        self.right.close()

    def next_batch(self, max_rows: int) -> Optional[Batch]:
        self._check_pullable()
        while len(self._pending) < max_rows and not self._left_eof:
            try:
                batch = self.left.next_batch(max_rows)
            except BaseException:
                self.close()
                raise
            if batch is None:
                self._left_eof = True
                break
            for row in batch:
                key = self.left_key(row)
                if key is None or any(v is None for v in key):
                    continue  # NULL never joins
                for match in self._table.get(key, ()):  # bag product m x n
                    self._pending.append(row + match)
            if not batch:
                break  # propagate "empty batch" upstream signal
        if not self._pending:
            return None if self._left_eof else []
        out: Batch = []
        while self._pending and len(out) < max_rows:
            out.append(self._pending.popleft())
        return out
