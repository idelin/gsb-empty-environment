"""Input stream abstraction.

A stream is a pull-based source of ``Batch`` objects whose rows are sorted
by the merge key.  ``poll()`` returns one of three things:

* a ``Batch`` with the rows available at the current position,
* ``NOT_READY`` -- the stream *temporarily* has no data (this is NOT eof),
* ``EOF_SIGNAL`` -- the stream is permanently exhausted.

The merger reports consumed rows via ``advance(n)``; ``offset`` is the total
number of rows consumed so far and is what gets stored in a checkpoint
cursor.  ``seek(offset)`` repositions the stream during recovery.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import Iterable

from .types import Batch


class _Signal:
    __slots__ = ("name",)

    def __init__(self, name: str) -> None:
        self.name = name

    def __repr__(self) -> str:
        return self.name


NOT_READY = _Signal("NOT_READY")
EOF_SIGNAL = _Signal("EOF")


class Stream(ABC):
    @property
    @abstractmethod
    def stream_id(self) -> str:
        ...

    @property
    @abstractmethod
    def version(self) -> int:
        ...

    @property
    @abstractmethod
    def offset(self) -> int:
        ...

    @abstractmethod
    def poll(self) -> "Batch | _Signal":
        ...

    @abstractmethod
    def advance(self, n: int) -> None:
        ...

    @abstractmethod
    def seek(self, offset: int) -> None:
        ...


class ListStream(Stream):
    """A ``Stream`` backed by an in-memory list of batches.

    ``not_ready_at`` is a set of 0-based poll-call indices that should
    report ``NOT_READY`` instead of data, simulating a stream that is
    temporarily empty in the middle of its lifetime.
    """

    def __init__(
        self,
        stream_id: str,
        batches: Iterable[Batch],
        *,
        version: int = 1,
        not_ready_at: Iterable[int] = (),
    ) -> None:
        self._stream_id = stream_id
        self._version = version
        self._batches = list(batches)
        self._starts: list[int] = []
        total = 0
        for batch in self._batches:
            self._starts.append(total)
            total += len(batch)
        self._total = total
        self._offset = 0
        self._poll_count = 0
        self._not_ready_at = set(not_ready_at)

    @property
    def stream_id(self) -> str:
        return self._stream_id

    @property
    def version(self) -> int:
        return self._version

    @property
    def offset(self) -> int:
        return self._offset

    def poll(self) -> "Batch | _Signal":
        poll_index = self._poll_count
        self._poll_count += 1
        if poll_index in self._not_ready_at:
            return NOT_READY
        if self._offset >= self._total:
            return EOF_SIGNAL
        batch_index, row_index = self._locate(self._offset)
        batch = self._batches[batch_index]
        return Batch(batch.columns, batch.types, batch.rows[row_index:])

    def advance(self, n: int) -> None:
        if n < 0 or self._offset + n > self._total:
            raise ValueError(f"cannot advance {n} rows from offset {self._offset}")
        self._offset += n

    def seek(self, offset: int) -> None:
        if not 0 <= offset <= self._total:
            raise ValueError(f"offset {offset} out of range [0, {self._total}]")
        self._offset = offset

    def _locate(self, offset: int) -> tuple[int, int]:
        for index, (start, batch) in enumerate(zip(self._starts, self._batches)):
            if offset < start + len(batch):
                return index, offset - start
        return len(self._batches), 0
