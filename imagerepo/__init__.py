"""Offline image repository (Python 3.11 standard library only)."""
from .closure import (
    CONTENT_MISSING,
    DIGEST_MISMATCH,
    METADATA_MISSING,
    ClosureResult,
    Gap,
    compute_closure,
)
from .journal import JournalCorrupt, scan_journal
from .model import HOST_PLATFORM, Dep, Ident, Metadata
from .repo import (
    CrashInjected,
    FaultBus,
    GcReport,
    ImmutabilityViolation,
    IncompleteSnapshot,
    LeaseInvalid,
    RepoError,
    Repository,
)

__all__ = [
    "HOST_PLATFORM", "Dep", "Ident", "Metadata", "Repository", "FaultBus",
    "CrashInjected", "RepoError", "IncompleteSnapshot", "ImmutabilityViolation",
    "LeaseInvalid", "GcReport", "compute_closure", "ClosureResult", "Gap",
    "METADATA_MISSING", "CONTENT_MISSING", "DIGEST_MISMATCH",
    "JournalCorrupt", "scan_journal",
]
