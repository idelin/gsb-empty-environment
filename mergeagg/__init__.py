"""Merge-aggregation over sorted typed batch streams with checkpoints."""

from .aggregator import AggFunc, AggSpec
from .checkpoint import Checkpoint, CursorState, PendingGroup
from .errors import (
    CorruptCheckpointError,
    CursorError,
    CursorVersionMismatchError,
    MergeAggError,
    StreamNotReadyError,
)
from .merger import MergeAggregator
from .streams import EOF_SIGNAL, NOT_READY, ListStream, Stream
from .types import NULL, Batch, ColumnType, is_nan, is_null

__all__ = [
    "AggFunc",
    "AggSpec",
    "Batch",
    "Checkpoint",
    "ColumnType",
    "CorruptCheckpointError",
    "CursorError",
    "CursorState",
    "CursorVersionMismatchError",
    "EOF_SIGNAL",
    "ListStream",
    "MergeAggError",
    "MergeAggregator",
    "NOT_READY",
    "NULL",
    "PendingGroup",
    "Stream",
    "StreamNotReadyError",
    "is_nan",
    "is_null",
]
