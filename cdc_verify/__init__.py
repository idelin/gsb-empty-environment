"""Verify-before-apply CDC reference model (stdlib only)."""

from .model import MISSING, ConflictRecord, Event, FieldConflict, Report
from .processor import Processor

__all__ = [
    "MISSING",
    "ConflictRecord",
    "Event",
    "FieldConflict",
    "Processor",
    "Report",
]
