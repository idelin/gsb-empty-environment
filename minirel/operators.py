"""Volcano-style pull operators over synthetic tables.

Semantics implemented on purpose:

* bag (multiset) semantics everywhere -- duplicates are kept, join
  multiplicities are the cartesian product of matching rows;
* NULL keys never join and NULL predicates (UNKNOWN) filter rows out;
* blocking operators (HashJoin build side, Aggregate, Sort) charge
  memory to the shared BudgetTracker;
* Sort spills to local temp files; close() (also after budget/cancel
  abort) always deletes them;
* Aggregate exposes *partial* state only through ``partial_result()``
  which is explicitly marked incomplete -- it can never be mistaken
  for a final aggregate value.
"""

from __future__ import annotations

import heapq
import os
import pickle
import tempfile
from dataclasses import dataclass, field

from .budget import BudgetExhausted, BudgetTracker, Cancelled
from .table import NULL, is_null

# Rough accounting unit: one materialised row costs this many bytes.
ROW_BYTES = 64


class Operator:
    children: tuple["Operator", ...] = ()

    def open(self) -> None:
        for child in self.children:
            child.open()

    def next(self) -> tuple | None:
        raise NotImplementedError

    def close(self) -> None:
        for child in self.children:
            child.close()

    def partial_result(self):
        """Return an explicitly-marked partial result if this operator
        (or a descendant) was interrupted mid-computation."""
        for child in self.children:
            partial = child.partial_result()
            if partial is not None:
                return partial
        return None


class Scan(Operator):
    def __init__(self, table, tracker: BudgetTracker) -> None:
        self.table = table
        self.tracker = tracker
        self._iter = None

    def open(self) -> None:
        self._iter = iter(self.table.rows)

    def next(self) -> tuple | None:
        self.tracker.check_cancelled()
        self.tracker.charge_steps()
        row = next(self._iter, None)
        if row is None:
            return None
        # The single choke point where scanned rows are paid for.
        self.tracker.charge_rows()
        return row


class Filter(Operator):
    def __init__(self, child: Operator, predicate) -> None:
        self.child = child
        self.children = (child,)
        self.predicate = predicate

    def next(self) -> tuple | None:
        while True:
            row = self.child.next()
            if row is None:
                return None
            # UNKNOWN (NULL) and FALSE both reject the row.
            if self.predicate(row) is True:
                return row


class Project(Operator):
    def __init__(self, child: Operator, funcs, names: tuple[str, ...] = ()) -> None:
        self.child = child
        self.children = (child,)
        self.funcs = funcs
        self.names = names

    def next(self) -> tuple | None:
        row = self.child.next()
        if row is None:
            return None
        return tuple(func(row) for func in self.funcs)


class HashJoin(Operator):
    """Inner hash join. NULL keys never match. Bag semantics."""

    def __init__(self, left: Operator, right: Operator,
                 left_key, right_key, tracker: BudgetTracker) -> None:
        self.left, self.right = left, right
        self.children = (left, right)
        self.left_key, self.right_key = left_key, right_key
        self.tracker = tracker
        self._table: dict = {}
        self._mem_paid = 0
        self._right_row = None
        self._matches: list = []

    def open(self) -> None:
        super().open()
        while True:
            row = self.left.next()
            if row is None:
                break
            key = self.left_key(row)
            if is_null(key):
                continue  # NULL never joins, but the row was still paid for
            self.tracker.charge_memory(ROW_BYTES)
            self._mem_paid += ROW_BYTES
            self._table.setdefault(key, []).append(row)

    def next(self) -> tuple | None:
        while True:
            if self._matches:
                left_row = self._matches.pop()
                return left_row + self._right_row
            self.tracker.check_cancelled()
            right_row = self.right.next()
            if right_row is None:
                return None
            key = self.right_key(right_row)
            if is_null(key):
                continue
            self._matches = list(self._table.get(key, ()))
            self._right_row = right_row

    def close(self) -> None:
        if self._mem_paid:
            self.tracker.release_memory(self._mem_paid)
            self._mem_paid = 0
        self._table = {}
        super().close()


class Limit(Operator):
    """Normal early termination: records ``hit`` without raising."""

    def __init__(self, child: Operator, count: int) -> None:
        self.child = child
        self.children = (child,)
        self.count = count
        self._emitted = 0
        self.hit = False

    def next(self) -> tuple | None:
        if self._emitted >= self.count:
            self.hit = True
            return None
        row = self.child.next()
        if row is None:
            return None
        self._emitted += 1
        return row


@dataclass
class PartialAggregate:
    """Interrupted aggregation. ``complete is False`` forever here;
    these values must never be presented as final aggregate results."""

    rows: list
    complete: bool = False

    def __post_init__(self) -> None:
        if self.complete:
            raise ValueError("PartialAggregate cannot be marked complete")


class Aggregate(Operator):
    """Blocking group-by aggregate.

    agg spec: list of (func_name, column_index_or_None):
      ("count", None) -> COUNT(*);  ("count", i) -> COUNT(col)
      ("sum"|"avg"|"min"|"max", i)
    NULL inputs are skipped per SQL; all-NULL/empty inputs yield NULL.
    """

    def __init__(self, child: Operator, group_keys, aggs,
                 tracker: BudgetTracker) -> None:
        self.child = child
        self.children = (child,)
        self.group_keys = group_keys
        self.aggs = aggs
        self.tracker = tracker
        self._groups: dict = {}
        self._finalized = False
        self._results: list = []
        self._pos = 0

    def open(self) -> None:
        super().open()
        while True:
            row = self.child.next()
            if row is None:
                break
            key = tuple(k(row) for k in self.group_keys)
            accs = self._groups.get(key)
            if accs is None:
                accs = [self._new_acc(name) for name, _ in self.aggs]
                self._groups[key] = accs
            for acc, (name, col) in zip(accs, self.aggs):
                self._accumulate(acc, name, col, row)
        self._results = [
            key + tuple(self._finish(acc, name)
                        for acc, (name, _) in zip(accs, self.aggs))
            for key, accs in sorted(self._groups.items(), key=str)
        ]
        self._finalized = True

    def next(self) -> tuple | None:
        if self._pos >= len(self._results):
            return None
        row = self._results[self._pos]
        self._pos += 1
        return row

    def partial_result(self):
        if self._finalized:
            return None
        return PartialAggregate(self._snapshot())

    def _snapshot(self) -> list:
        return [
            key + tuple(self._finish(acc, name)
                        for acc, (name, _) in zip(accs, self.aggs))
            for key, accs in sorted(self._groups.items(), key=str)
        ]

    @staticmethod
    def _new_acc(name: str):
        if name == "count":
            return [0]
        if name == "avg":
            return [0, 0]  # [sum, non-null count]
        return [None]      # sum / min / max

    @staticmethod
    def _accumulate(acc, name: str, col, row) -> None:
        if name == "count":
            if col is None or not is_null(row[col]):
                acc[0] += 1
            return
        value = row[col]
        if is_null(value):
            return
        if name == "sum":
            acc[0] = value if acc[0] is None else acc[0] + value
        elif name == "avg":
            acc[0] += value
            acc[1] += 1
        elif name == "min":
            acc[0] = value if acc[0] is None else min(acc[0], value)
        elif name == "max":
            acc[0] = value if acc[0] is None else max(acc[0], value)

    @staticmethod
    def _finish(acc, name: str):
        if name == "count":
            return acc[0]
        if name == "avg":
            return acc[0] / acc[1] if acc[1] else NULL
        return acc[0] if acc[0] is not None else NULL


class Sort(Operator):
    """Blocking sort that spills chunks to temp files.

    close() unconditionally deletes every spill file, including when
    open() is aborted by BudgetExhausted or Cancelled.
    """

    def __init__(self, child: Operator, key, tracker: BudgetTracker,
                 tempdir: str, spill_after: int = 1000) -> None:
        self.child = child
        self.children = (child,)
        self.key = key
        self.tracker = tracker
        self.tempdir = tempdir
        self.spill_after = spill_after
        self._buffer: list = []
        self._tmpfiles: list[str] = []
        self._mem_paid = 0
        self._merged = None

    def open(self) -> None:
        super().open()
        while True:
            row = self.child.next()
            if row is None:
                break
            if len(self._buffer) >= self.spill_after:
                self._spill()
            self.tracker.charge_memory(ROW_BYTES)
            self._mem_paid += ROW_BYTES
            self._buffer.append(row)
        self._buffer.sort(key=self.key)
        self.tracker.release_memory(self._mem_paid)
        self._mem_paid = 0
        streams = [self._read_spill(path) for path in self._tmpfiles]
        self._merged = heapq.merge(self._buffer, *streams, key=self.key)

    def next(self) -> tuple | None:
        self.tracker.check_cancelled()
        row = next(self._merged, None)
        if row is None:
            return None
        # Re-reading spilled rows costs steps, not new row scans.
        self.tracker.charge_steps()
        return row

    def _spill(self) -> None:
        self._buffer.sort(key=self.key)
        fd, path = tempfile.mkstemp(prefix="minirel_sort_", dir=self.tempdir)
        with os.fdopen(fd, "wb") as handle:
            pickle.dump(self._buffer, handle, protocol=pickle.HIGHEST_PROTOCOL)
        self._tmpfiles.append(path)
        self.tracker.release_memory(self._mem_paid)
        self._mem_paid = 0
        self._buffer = []

    def _read_spill(self, path: str):
        with open(path, "rb") as handle:
            for row in pickle.load(handle):
                yield row

    def close(self) -> None:
        for path in self._tmpfiles:
            try:
                os.remove(path)
            except FileNotFoundError:
                pass
        self._tmpfiles.clear()
        if self._mem_paid:
            self.tracker.release_memory(self._mem_paid)
            self._mem_paid = 0
        super().close()


class SharedSubplan:
    """Execute a subplan once and let several consumers share both
    its output and its budget charges ("shared plan node").

    Rows are produced by the child (and therefore charged to the
    shared tracker) exactly once; consumers only read the cached copy.
    """

    def __init__(self, child: Operator, tracker: BudgetTracker) -> None:
        self.child = child
        self.tracker = tracker
        self._cache: list[tuple] = []
        self._mem_paid = 0
        self._opened = False
        self._exhausted = False
        self._closed = False

    def consumer(self) -> "SharedConsumer":
        return SharedConsumer(self)

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        self.child.close()
        if self._mem_paid:
            self.tracker.release_memory(self._mem_paid)
            self._mem_paid = 0


class SharedConsumer(Operator):
    def __init__(self, shared: SharedSubplan) -> None:
        self.shared = shared
        self._pos = 0

    def open(self) -> None:
        if not self.shared._opened:
            self.shared._opened = True
            self.shared.child.open()

    def next(self) -> tuple | None:
        shared = self.shared
        if self._pos < len(shared._cache):
            row = shared._cache[self._pos]
        elif shared._exhausted:
            return None
        else:
            row = shared.child.next()  # budget charged exactly once
            if row is None:
                shared._exhausted = True
                return None
            shared.tracker.charge_memory(ROW_BYTES)
            shared._mem_paid += ROW_BYTES
            shared._cache.append(row)
        self._pos += 1
        return row

    def close(self) -> None:
        self.shared.close()
