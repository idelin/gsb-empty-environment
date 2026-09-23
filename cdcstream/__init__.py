"""Deterministic CDC test-stream toolkit.

Generates deterministic delivery streams (transactions, duplicates,
out-of-order, crash points) from local fixtures, validates an incremental
processor against an independent full-state oracle, separates invalid
streams from processing errors, and shrinks failing streams while keeping
transaction structure replayable.
"""

from .model import ChangeEvent, CrashPoint, DeliveryItem, events_of, format_stream
from .fixtures import FixtureError, group_transactions, load_events
from .generator import StreamGenerator
from .processor import ArrivalOrderProcessor, IncrementalProcessor
from .oracle import full_state
from .validator import (
    InvalidStreamError,
    ProcessingError,
    ValidationResult,
    run_and_validate,
    validate_stream,
)
from .shrinker import shrink

__all__ = [
    "ArrivalOrderProcessor",
    "ChangeEvent",
    "CrashPoint",
    "DeliveryItem",
    "FixtureError",
    "IncrementalProcessor",
    "InvalidStreamError",
    "ProcessingError",
    "StreamGenerator",
    "ValidationResult",
    "events_of",
    "format_stream",
    "full_state",
    "group_transactions",
    "load_events",
    "run_and_validate",
    "shrink",
    "validate_stream",
]
