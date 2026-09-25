"""Exception hierarchy for the merge-aggregation engine."""

from __future__ import annotations


class MergeAggError(Exception):
    """Base class for all errors raised by this package."""


class CursorError(MergeAggError):
    """A checkpoint cursor does not match the provided streams."""


class CursorVersionMismatchError(CursorError):
    """A stream's version differs from the version stored in the checkpoint."""

    def __init__(self, stream_id: str, expected: int, actual: int) -> None:
        super().__init__(
            f"cursor version mismatch for stream {stream_id!r}: "
            f"checkpoint has version {expected}, stream has version {actual}"
        )
        self.stream_id = stream_id
        self.expected = expected
        self.actual = actual


class StreamNotReadyError(MergeAggError):
    """A stream stayed in the NOT_READY state beyond the retry budget."""


class CorruptCheckpointError(MergeAggError):
    """Checkpoint contents are inconsistent with the stream data."""
