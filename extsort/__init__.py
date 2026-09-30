"""Bounded-memory external merge sort with manifest-based crash recovery."""

from .sorter import (
    CorruptRunError,
    ExternalSorter,
    ManifestCorruptError,
    SortInterrupted,
)

__all__ = [
    "CorruptRunError",
    "ExternalSorter",
    "ManifestCorruptError",
    "SortInterrupted",
]
__version__ = "0.1.0"
