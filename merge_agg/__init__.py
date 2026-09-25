"""Merge-aggregation over sorted input streams with checkpoint/resume."""

from .aggregate import FUNCS, Accumulator
from .batch import Batch, Schema, SchemaError, is_nan
from .merger import (
    COMPLETE,
    PAUSED,
    CursorVersionMismatch,
    MergeAggregator,
    MergerError,
    UnsortedInputError,
)
from .stream import Cursor, ListStream, StreamError

__all__ = [
    "Accumulator",
    "Batch",
    "COMPLETE",
    "Cursor",
    "CursorVersionMismatch",
    "FUNCS",
    "ListStream",
    "MergeAggregator",
    "MergerError",
    "PAUSED",
    "Schema",
    "SchemaError",
    "StreamError",
    "UnsortedInputError",
    "is_nan",
]
