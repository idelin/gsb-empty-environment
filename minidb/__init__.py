"""Miniature relational store with atomic primary-key changes.

Public API:
    Database, ForeignKey, Transaction
    ConflictError, UnknownKeyError, FrozenSnapshotError
    verify
    Event, load_events, apply_events, ApplyReport
"""

from .core import (
    ConflictError,
    Database,
    ForeignKey,
    FrozenSnapshotError,
    Transaction,
    UnknownKeyError,
)
from .fixtures import ApplyReport, Event, apply_events, load_events
from .verifier import verify

__all__ = [
    "ApplyReport",
    "ConflictError",
    "Database",
    "Event",
    "ForeignKey",
    "FrozenSnapshotError",
    "Transaction",
    "UnknownKeyError",
    "apply_events",
    "load_events",
    "verify",
]
