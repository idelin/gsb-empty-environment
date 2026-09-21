"""Run-length encoded device-state history with source provenance."""

from .model import Patch, Record, RecordKey, Resolver, Segment
from .segmenter import Conflict, Run, build_runs, find_conflicts
from .store import HistoryStore
from .view import SegmentedView

__all__ = [
    "Conflict",
    "HistoryStore",
    "Patch",
    "Record",
    "RecordKey",
    "Resolver",
    "Run",
    "Segment",
    "SegmentedView",
    "build_runs",
    "find_conflicts",
]
