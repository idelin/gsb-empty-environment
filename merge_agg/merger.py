"""K-way merge aggregation over sorted streams, with checkpoint/resume.

The merger consumes rows in non-decreasing key order from every input
stream, groups rows by key, and folds each group into aggregate results.

Pause/resume model:

* ``run(sink)`` returns ``PAUSED`` when some stream is *temporarily*
  empty (``poll()`` returned ``None`` while not at eof).  A temporarily
  empty stream is never treated as permanently exhausted: the current
  (possibly incomplete) group is kept open and finalised only once every
  stream's head is known.
* ``save_checkpoint(path)`` persists everything needed to resume: the
  per-stream cursors (rows consumed + stream data version), the
  incomplete group's partial aggregates, and the last committed key.
* ``MergeAggregator.restore(path, streams)`` rebuilds the merger on top
  of equivalent streams.  A stream whose ``version`` differs from the
  checkpointed cursor raises :class:`CursorVersionMismatch`.  Groups
  committed before the checkpoint are never emitted again, because the
  cursors position every stream past the rows that produced them.
"""

from __future__ import annotations

import pickle
from collections import deque

from .aggregate import FUNCS, Accumulator
from .batch import is_nan
from .stream import Cursor

COMPLETE = "complete"
PAUSED = "paused"

CHECKPOINT_FORMAT_VERSION = 1


class MergerError(Exception):
    """Base class for merge/aggregate failures."""


class CursorVersionMismatch(MergerError):
    """A stream's data version does not match the checkpointed cursor."""


class UnsortedInputError(MergerError):
    """An input stream delivered rows out of key order."""


class MergeAggregator:
    """Merge-aggregate rows from sorted input streams by a key column.

    ``aggregations`` is a list of ``(func, column)`` pairs where ``func``
    is one of ``sum``, ``count``, ``min``, ``max``, ``avg``.  Each
    committed group is appended to the sink as ``(key, [result, ...])``.
    """

    def __init__(self, streams, key_column, aggregations):
        if not streams:
            raise MergerError("at least one input stream is required")
        schema = streams[0].schema
        for stream in streams:
            if stream.schema != schema:
                raise MergerError("all streams must share the same schema")
        self._schema = schema
        self._key_column = key_column
        self._key_idx = schema.index(key_column)
        self._aggregations = [(func, col) for func, col in aggregations]
        self._acc_spec = []
        for func, col in self._aggregations:
            if func not in FUNCS:
                raise MergerError(f"unknown aggregate {func!r}")
            col_type = schema.type_of(col)
            if func in ("sum", "avg") and col_type not in ("int", "float"):
                raise MergerError(
                    f"{func} requires a numeric column, {col!r} is {col_type}")
            if func in ("min", "max") and col_type not in ("int", "float", "str"):
                raise MergerError(
                    f"{func} does not support {col_type} column {col!r}")
            self._acc_spec.append((func, schema.index(col), col_type))
        self._streams = list(streams)
        n = len(streams)
        self._buffers = [deque() for _ in range(n)]
        self._consumed = [0] * n
        self._prev_keys = [None] * n
        self._has_group = False
        self._current_key = None
        self._current_accs = None
        self.committed_count = 0
        self.last_committed_key = None
        self._done = False

    # -- key handling ---------------------------------------------------

    def _validate_key(self, key):
        if key is None:
            raise MergerError("null keys are not supported")
        if is_nan(key):
            raise MergerError("NaN keys are not supported")

    def _check_sorted(self, i, key):
        prev = self._prev_keys[i]
        if prev is not None and key < prev:
            raise UnsortedInputError(
                f"stream {self._streams[i].stream_id!r}: key {key!r} "
                f"follows greater key {prev!r}")
        self._prev_keys[i] = key

    # -- stream heads ---------------------------------------------------

    def _head_known(self, i):
        """Ensure stream i's head row (or its exhaustion) is determined.

        Returns False iff the stream is temporarily empty: no buffered
        rows, ``poll()`` gave nothing, and the stream is not at eof.
        """
        stream = self._streams[i]
        while not self._buffers[i]:
            batch = stream.poll()
            if batch is None:
                # Closed + drained means permanently exhausted (head known);
                # open + drained means temporarily empty (head unknown).
                return stream.eof
            for row in batch.rows:
                self._validate_key(row[self._key_idx])
            self._buffers[i].extend(batch.rows)
        return True

    # -- main loop ------------------------------------------------------

    def run(self, sink):
        """Consume input until completion or a temporary-empty pause.

        Committed groups are appended to ``sink``.  Returns COMPLETE or
        PAUSED; after PAUSED the merger may be checkpointed and/or run
        again once more input arrives.
        """
        if self._done:
            return COMPLETE
        n = len(self._streams)
        key_idx = self._key_idx
        while True:
            for i in range(n):
                if not self._head_known(i):
                    return PAUSED
            if not self._has_group:
                candidates = [
                    self._buffers[i][0][key_idx]
                    for i in range(n)
                    if self._buffers[i]
                ]
                if not candidates:
                    self._done = True
                    return COMPLETE
                key = min(candidates)
                if (self.last_committed_key is not None
                        and key <= self.last_committed_key):
                    raise UnsortedInputError(
                        f"key {key!r} not greater than last committed key "
                        f"{self.last_committed_key!r}")
                self._current_key = key
                self._current_accs = [
                    Accumulator(func, col_type)
                    for func, _, col_type in self._acc_spec
                ]
                self._has_group = True
            key = self._current_key
            for i in range(n):
                while True:
                    buf = self._buffers[i]
                    while buf and buf[0][key_idx] == key:
                        row = buf.popleft()
                        self._check_sorted(i, row[key_idx])
                        self._consumed[i] += 1
                        for acc, (_, col_idx, _) in zip(
                                self._current_accs, self._acc_spec):
                            acc.add(row[col_idx])
                    if not self._head_known(i):
                        # Temporarily empty mid-group: pause with the partial
                        # group kept in state (and in any checkpoint).
                        return PAUSED
                    if not buf:
                        break  # stream permanently exhausted
                    head_key = buf[0][key_idx]
                    if head_key < key:
                        raise UnsortedInputError(
                            f"stream {self._streams[i].stream_id!r}: key "
                            f"{head_key!r} arrived after group {key!r} "
                            f"started")
                    if head_key > key:
                        break
                    # head_key == key: a refilled batch continues this group.
            sink.append((key, [acc.result() for acc in self._current_accs]))
            self.committed_count += 1
            self.last_committed_key = key
            self._has_group = False
            self._current_key = None
            self._current_accs = None

    # -- checkpoint / restore -------------------------------------------

    def checkpoint(self):
        """Return a picklable snapshot of the full resumable state."""
        return {
            "format_version": CHECKPOINT_FORMAT_VERSION,
            "key_column": self._key_column,
            "aggregations": list(self._aggregations),
            "cursors": [
                Cursor(stream.stream_id, stream.version, self._consumed[i])
                for i, stream in enumerate(self._streams)
            ],
            "has_group": self._has_group,
            "current_key": self._current_key,
            "current_accs": self._current_accs,
            "committed_count": self.committed_count,
            "last_committed_key": self.last_committed_key,
            "prev_keys": list(self._prev_keys),
            "done": self._done,
        }

    def save_checkpoint(self, path):
        with open(path, "wb") as fh:
            pickle.dump(self.checkpoint(), fh)

    @classmethod
    def restore(cls, path, streams):
        """Rebuild a merger from a checkpoint file over equivalent streams.

        Each stream is seeked to its checkpointed cursor.  Raises
        :class:`CursorVersionMismatch` if a stream's data version differs
        from the version recorded in its cursor.
        """
        with open(path, "rb") as fh:
            state = pickle.load(fh)
        if state.get("format_version") != CHECKPOINT_FORMAT_VERSION:
            raise MergerError(
                f"unsupported checkpoint format version "
                f"{state.get('format_version')!r}")
        merger = cls(streams, state["key_column"], state["aggregations"])
        cursors = state["cursors"]
        if len(cursors) != len(merger._streams):
            raise MergerError(
                f"checkpoint has {len(cursors)} cursors but "
                f"{len(merger._streams)} streams were provided")
        for i, (stream, cursor) in enumerate(zip(merger._streams, cursors)):
            if stream.stream_id != cursor.stream_id:
                raise MergerError(
                    f"stream #{i}: id {stream.stream_id!r} does not match "
                    f"checkpointed cursor id {cursor.stream_id!r}")
            if stream.version != cursor.version:
                raise CursorVersionMismatch(
                    f"stream {stream.stream_id!r}: cursor version "
                    f"{cursor.version} does not match stream version "
                    f"{stream.version}")
            stream.seek(cursor.rows_consumed)
            merger._consumed[i] = cursor.rows_consumed
        merger._has_group = state["has_group"]
        merger._current_key = state["current_key"]
        merger._current_accs = state["current_accs"]
        merger.committed_count = state["committed_count"]
        merger.last_committed_key = state["last_committed_key"]
        merger._prev_keys = state["prev_keys"]
        merger._done = state["done"]
        return merger
