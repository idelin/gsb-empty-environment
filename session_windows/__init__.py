"""Session-window changelog library.

Public API: build a :class:`SessionTracker`, feed it ``Upsert`` /
``Retract`` / ``AdvanceWatermark`` ops one at a time (``apply``) or in
batches (``apply_all``), and consume the retract/upsert/finalize
``Change`` stream.  ``ReferenceSessionTracker`` is a brute-force oracle
with identical semantics for testing.
"""

from .model import (
    AdvanceWatermark,
    BatchResult,
    Change,
    ChangeKind,
    Disposition,
    Event,
    Member,
    Op,
    OpResult,
    Retract,
    Upsert,
    Window,
    make_window,
)
from .reference import ReferenceSessionTracker
from .tracker import SessionTracker

__all__ = [
    "AdvanceWatermark",
    "BatchResult",
    "Change",
    "ChangeKind",
    "Disposition",
    "Event",
    "Member",
    "Op",
    "OpResult",
    "ReferenceSessionTracker",
    "Retract",
    "SessionTracker",
    "Upsert",
    "Window",
    "make_window",
]

__version__ = "0.1.0"
