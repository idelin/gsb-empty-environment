"""Transactional input/output pipeline with idempotent output and crash recovery.

The package implements the transactional-outbox pattern over local change
fixtures, using only the Python standard library (sqlite3 for durable state).
"""

from .errors import (
    PipelineError,
    GapDetectedError,
    StaleBatchError,
    DuplicateChangeError,
    ConflictingChangeError,
    PlannerError,
    SinkError,
    DeliveryFailedError,
    AckLostError,
    CrashInjected,
)
from .model import Change, Output, change_uid, output_id, load_changes
from .store import Store, PendingOutput
from .sink import LocalSink, DeliveryResult
from .processor import Processor, IngestReport, SendReport

__all__ = [
    "PipelineError",
    "GapDetectedError",
    "StaleBatchError",
    "DuplicateChangeError",
    "ConflictingChangeError",
    "PlannerError",
    "SinkError",
    "DeliveryFailedError",
    "AckLostError",
    "CrashInjected",
    "Change",
    "Output",
    "change_uid",
    "output_id",
    "load_changes",
    "Store",
    "PendingOutput",
    "LocalSink",
    "DeliveryResult",
    "Processor",
    "IngestReport",
    "SendReport",
]
