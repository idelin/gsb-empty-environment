"""K-way merge aggregation over sorted typed batch streams.

The merger assumes every input stream is sorted by the key column.  It
walks the streams in lockstep, groups rows by equal keys, and folds each
group through the requested aggregate states.

Pause / checkpoint / resume
---------------------------
``run(pause_after_rows=n)`` stops after consuming ``n`` rows, possibly in
the middle of a group that spans several streams.  At any such point
``checkpoint()`` captures:

* each stream's cursor ``(version, offset)``,
* the in-flight group (key, partial aggregate states, which streams have
  already been drained for that key),
* the committed-key watermark.

``MergeAggregator.restore(checkpoint, streams, ...)`` validates cursor
versions, seeks the streams, and continues exactly where the previous
merger stopped.  Groups already committed are never emitted again: their
rows are behind the cursors, and the watermark filters anything at or
below ``committed_upto`` as a second line of defense.

Temporary emptiness
-------------------
``poll()`` returning ``NOT_READY`` is never treated as end-of-stream.  The
merger waits (``retry_sleep`` via ``sleep_fn``) and polls again, up to
``max_not_ready`` consecutive attempts, then raises
``StreamNotReadyError``.  Only ``EOF_SIGNAL`` ends a stream.
"""

from __future__ import annotations

import time
from typing import Any, Callable, Iterable, Sequence

from .aggregator import AggSpec, AggState, make_state, state_from_json
from .checkpoint import Checkpoint, CursorState, PendingGroup
from .errors import (
    CorruptCheckpointError,
    CursorError,
    CursorVersionMismatchError,
    StreamNotReadyError,
)
from .streams import EOF_SIGNAL, NOT_READY, Stream
from .types import Batch, is_nan, is_null


class MergeAggregator:
    def __init__(
        self,
        streams: Sequence[Stream],
        key_col: str,
        agg_specs: Iterable[AggSpec],
        *,
        max_not_ready: int = 1000,
        retry_sleep: float = 0.05,
        sleep_fn: Callable[[float], None] = time.sleep,
    ) -> None:
        self._streams = list(streams)
        if not self._streams:
            raise ValueError("at least one stream is required")
        ids = [s.stream_id for s in self._streams]
        if len(set(ids)) != len(ids):
            raise ValueError(f"duplicate stream ids: {ids!r}")
        self._key_col = key_col
        self._specs = list(agg_specs)
        if not self._specs:
            raise ValueError("at least one aggregate spec is required")
        self._max_not_ready = max_not_ready
        self._retry_sleep = retry_sleep
        self._sleep_fn = sleep_fn

        n = len(self._streams)
        self._batches: list[Batch | None] = [None] * n
        self._row = [0] * n
        self._eof = [False] * n
        self._key_idx: list[int | None] = [None] * n
        self._col_idx: list[list[int] | None] = [None] * n

        self._has_pending = False
        self._pending_key: Any = None
        self._pending_states: list[AggState] = []
        self._pending_drained: set[int] = set()

        self._has_committed = False
        self._committed_upto: Any = None

        self.outputs: list[dict] = []

    # ------------------------------------------------------------------
    # public API
    # ------------------------------------------------------------------

    def run(self, pause_after_rows: int | None = None) -> list[dict]:
        """Consume rows until all streams hit EOF (or the pause limit).

        Returns the cumulative list of output rows.  May be called again
        after a pause to continue.
        """
        consumed = 0
        while True:
            frontier = self._frontier()
            if self._has_pending:
                if frontier is None or frontier > self._pending_key:
                    self._emit_pending()
                    if frontier is None:
                        break
                    continue
                if frontier < self._pending_key:
                    raise CorruptCheckpointError(
                        f"frontier key {frontier!r} is behind pending group "
                        f"key {self._pending_key!r}"
                    )
            else:
                if frontier is None:
                    break
                self._begin_pending(frontier)

            paused = False
            for index in range(len(self._streams)):
                if index in self._pending_drained:
                    continue
                if not self._ensure_head(index):
                    self._pending_drained.add(index)
                    continue
                while self._ensure_head(index) and self._head_key(index) == self._pending_key:
                    self._consume(index)
                    consumed += 1
                    if pause_after_rows is not None and consumed >= pause_after_rows:
                        paused = True
                        break
                if paused:
                    break
                self._pending_drained.add(index)
            if paused:
                break
        return self.outputs

    def checkpoint(self) -> Checkpoint:
        cursors = {
            s.stream_id: CursorState(s.stream_id, s.version, s.offset)
            for s in self._streams
        }
        pending = None
        if self._has_pending:
            pending = PendingGroup(
                key=self._pending_key,
                states=tuple(st.to_json() for st in self._pending_states),
                drained=tuple(
                    self._streams[i].stream_id for i in sorted(self._pending_drained)
                ),
            )
        return Checkpoint(
            cursors=cursors,
            pending=pending,
            committed_upto=self._committed_upto,
            has_committed=self._has_committed,
        )

    @classmethod
    def restore(
        cls,
        checkpoint: Checkpoint,
        streams: Sequence[Stream],
        key_col: str,
        agg_specs: Iterable[AggSpec],
        **run_kwargs: Any,
    ) -> "MergeAggregator":
        streams = list(streams)
        by_id: dict[str, Stream] = {}
        for stream in streams:
            if stream.stream_id in by_id:
                raise CursorError(f"duplicate stream id {stream.stream_id!r}")
            by_id[stream.stream_id] = stream
        missing = set(checkpoint.cursors) - set(by_id)
        extra = set(by_id) - set(checkpoint.cursors)
        if missing or extra:
            raise CursorError(
                f"stream set mismatch: missing={sorted(missing)!r} "
                f"extra={sorted(extra)!r}"
            )
        for stream_id, cursor in checkpoint.cursors.items():
            stream = by_id[stream_id]
            if stream.version != cursor.version:
                raise CursorVersionMismatchError(
                    stream_id, cursor.version, stream.version
                )
            stream.seek(cursor.offset)

        merger = cls(streams, key_col, agg_specs, **run_kwargs)
        if checkpoint.pending is not None:
            pending = checkpoint.pending
            if len(pending.states) != len(merger._specs):
                raise CorruptCheckpointError(
                    f"pending group has {len(pending.states)} aggregate states, "
                    f"expected {len(merger._specs)}"
                )
            index_of = {s.stream_id: i for i, s in enumerate(merger._streams)}
            try:
                drained = {index_of[sid] for sid in pending.drained}
            except KeyError as exc:
                raise CorruptCheckpointError(
                    f"pending group references unknown stream {exc}"
                ) from None
            merger._has_pending = True
            merger._pending_key = pending.key
            merger._pending_states = [state_from_json(d) for d in pending.states]
            merger._pending_drained = drained
        merger._committed_upto = checkpoint.committed_upto
        merger._has_committed = checkpoint.has_committed
        return merger

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------

    def _map_columns(self, index: int, batch: Batch) -> None:
        stream_id = self._streams[index].stream_id
        try:
            self._key_idx[index] = batch.column_index(self._key_col)
        except ValueError:
            raise ValueError(
                f"stream {stream_id!r} is missing key column {self._key_col!r}"
            ) from None
        col_idx = []
        for spec in self._specs:
            try:
                col_idx.append(batch.column_index(spec.column))
            except ValueError:
                raise ValueError(
                    f"stream {stream_id!r} is missing aggregate column "
                    f"{spec.column!r}"
                ) from None
        self._col_idx[index] = col_idx

    def _ensure_head(self, index: int) -> bool:
        """Make sure stream ``index`` has a current row, unless it is EOF."""
        not_ready = 0
        while True:
            if self._eof[index]:
                return False
            batch = self._batches[index]
            if batch is not None and self._row[index] < len(batch):
                return True
            polled = self._streams[index].poll()
            if polled is NOT_READY:
                not_ready += 1
                if not_ready > self._max_not_ready:
                    raise StreamNotReadyError(
                        f"stream {self._streams[index].stream_id!r} stayed "
                        f"NOT_READY for {not_ready} consecutive polls"
                    )
                self._sleep_fn(self._retry_sleep)
                continue
            not_ready = 0
            if polled is EOF_SIGNAL:
                self._eof[index] = True
                self._batches[index] = None
                return False
            if not isinstance(polled, Batch):
                raise TypeError(
                    f"stream {self._streams[index].stream_id!r} returned "
                    f"unexpected object {polled!r}"
                )
            if len(polled) == 0:
                continue
            self._map_columns(index, polled)
            self._batches[index] = polled
            self._row[index] = 0

    def _head_key(self, index: int) -> Any:
        batch = self._batches[index]
        assert batch is not None and self._key_idx[index] is not None
        value = batch.row(self._row[index])[self._key_idx[index]]
        if is_null(value) or is_nan(value):
            raise ValueError(
                f"stream {self._streams[index].stream_id!r}: sort key must "
                f"not be NULL or NaN, got {value!r}"
            )
        return value

    def _frontier(self) -> Any:
        keys = []
        for index in range(len(self._streams)):
            if self._ensure_head(index):
                keys.append(self._head_key(index))
        return min(keys) if keys else None

    def _begin_pending(self, key: Any) -> None:
        self._has_pending = True
        self._pending_key = key
        self._pending_states = [make_state(spec.func) for spec in self._specs]
        self._pending_drained = set()

    def _consume(self, index: int) -> None:
        batch = self._batches[index]
        assert batch is not None and self._col_idx[index] is not None
        row = batch.row(self._row[index])
        col_idx = self._col_idx[index]
        for state, column in zip(self._pending_states, col_idx):
            state.add(row[column])
        self._row[index] += 1
        self._streams[index].advance(1)

    def _emit_pending(self) -> None:
        if not self._has_committed or self._pending_key > self._committed_upto:
            output = {"key": self._pending_key}
            for spec, state in zip(self._specs, self._pending_states):
                output[spec.name] = state.result()
            self.outputs.append(output)
            self._committed_upto = self._pending_key
            self._has_committed = True
        self._has_pending = False
        self._pending_key = None
        self._pending_states = []
        self._pending_drained = set()
