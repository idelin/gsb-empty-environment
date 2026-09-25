"""Input streams of sorted batches, with cursors for checkpointing.

A stream distinguishes two conditions that query engines often conflate:

* ``poll()`` returning ``None`` -- *temporarily* no data available; more
  batches may arrive later (e.g. via ``append``).  This is NOT eof.
* ``eof`` -- the stream was permanently closed via ``close()``; no more
  data will ever arrive.

A :class:`Cursor` identifies a position in a stream by the number of
rows consumed so far, plus the stream's data ``version``.  Restoring a
cursor onto a stream whose version differs is an error, because the
underlying data may have changed and positions would be meaningless.
"""

from __future__ import annotations

from .batch import Batch


class StreamError(Exception):
    """Base class for stream failures."""


class Cursor:
    """Position of one input stream: rows consumed + data version."""

    __slots__ = ("stream_id", "version", "rows_consumed")

    def __init__(self, stream_id, version, rows_consumed):
        self.stream_id = stream_id
        self.version = version
        self.rows_consumed = rows_consumed

    def __eq__(self, other):
        return (
            isinstance(other, Cursor)
            and self.stream_id == other.stream_id
            and self.version == other.version
            and self.rows_consumed == other.rows_consumed
        )

    def __repr__(self):
        return (
            f"Cursor(stream_id={self.stream_id!r}, version={self.version!r}, "
            f"rows_consumed={self.rows_consumed!r})"
        )


class ListStream:
    """A stream backed by an appendable in-memory list of batches.

    Rows must be appended in non-decreasing key order (enforced by the
    merger, not here).  ``version`` identifies the generation of the
    stream's data; bumping it invalidates previously saved cursors.
    """

    def __init__(self, stream_id, schema, batches=(), version=1):
        self.stream_id = stream_id
        self.schema = schema
        self.version = version
        self._batches = []
        self._closed = False
        self._skip = 0  # rows to skip before the next poll (set by seek)
        for batch in batches:
            self.append(batch)

    def append(self, batch: Batch):
        if self._closed:
            raise StreamError(f"stream {self.stream_id!r} is closed")
        if batch.schema != self.schema:
            raise StreamError(
                f"stream {self.stream_id!r}: batch schema does not match")
        self._batches.append(batch)

    def close(self):
        """Mark permanent end-of-input."""
        self._closed = True

    @property
    def eof(self) -> bool:
        return self._closed

    def seek(self, rows_consumed: int):
        """Position the stream so the first ``rows_consumed`` rows are skipped."""
        if rows_consumed < 0:
            raise StreamError("negative seek")
        self._skip = rows_consumed

    def poll(self):
        """Return the next unconsumed batch, or None if temporarily empty."""
        while self._batches and self._skip >= len(self._batches[0].rows):
            self._skip -= len(self._batches[0].rows)
            self._batches.pop(0)
        if not self._batches:
            if self._skip:
                if self._closed:
                    raise StreamError(
                        f"stream {self.stream_id!r}: cursor points beyond "
                        f"available data ({self._skip} rows missing)")
                return None  # data for the cursor position not appended yet
            return None
        batch = self._batches.pop(0)
        if self._skip:
            batch = Batch(self.schema, batch.rows[self._skip:])
            self._skip = 0
        return batch
