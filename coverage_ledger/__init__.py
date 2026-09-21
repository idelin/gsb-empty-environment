"""Correctable coverage ledger.

Tiny, standard-library-only toolkit for device online coverage over time,
with membership rule versions, invertible revisions and pinned snapshots.
"""

from .errors import (
    DuplicateReport,
    InvalidInterval,
    InvalidWindow,
    LedgerError,
    UnknownRecord,
)
from .ledger import Ledger, Revision, StateView
from .persistence import from_json, load_checkpoint, save_checkpoint, to_json
from .query import QueryResult, Segment, query
from .report import Delta, reconcile, revision_impact, run_current, run_published

__all__ = [
    "Ledger",
    "Revision",
    "StateView",
    "QueryResult",
    "Segment",
    "Delta",
    "LedgerError",
    "InvalidWindow",
    "InvalidInterval",
    "DuplicateReport",
    "UnknownRecord",
    "query",
    "reconcile",
    "revision_impact",
    "run_published",
    "run_current",
    "save_checkpoint",
    "load_checkpoint",
    "to_json",
    "from_json",
]
