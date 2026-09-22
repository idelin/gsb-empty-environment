"""Public data types for the incremental session-window store."""

from __future__ import annotations

import enum
from dataclasses import dataclass
from typing import Any, Union

__all__ = [
    "Event",
    "Retract",
    "Operation",
    "Member",
    "Window",
    "Upsert",
    "RetractWindow",
    "CloseWindow",
    "Change",
    "Disposition",
    "ApplyResult",
    "BatchResult",
]


# ---------------------------------------------------------------------------
# Input operations
# ---------------------------------------------------------------------------

@dataclass(frozen=True, slots=True)
class Event:
    """An observation. ``(source, seq)`` is the durable identity of the event."""

    source: str
    seq: int
    event_ms: int
    value: Any = None


@dataclass(frozen=True, slots=True)
class Retract:
    """Withdraws the event previously identified by ``(source, seq)``."""

    source: str
    seq: int


Operation = Union[Event, Retract]


# ---------------------------------------------------------------------------
# Windows
# ---------------------------------------------------------------------------

@dataclass(frozen=True, slots=True)
class Member:
    seq: int
    event_ms: int
    value: Any


@dataclass(frozen=True, slots=True)
class Window:
    """A session: a maximal run of events of one source with gaps <= gap_ms.

    ``id`` is stable across in-place growth of the session.  Continuity rules
    for merges and splits are documented in README.md and enforced by the
    store:

    * merge  -> the merged window keeps the id of the window with the
                smallest ``start_ms``; the other ids are retracted;
    * split  -> the fragment containing the original ``start_ms`` keeps the
                id; every other fragment is a brand-new id.
    """

    id: int
    source: str
    start_ms: int
    end_ms: int
    members: tuple[Member, ...]

    @property
    def count(self) -> int:
        return len(self.members)


# ---------------------------------------------------------------------------
# Changes (the retract/upsert stream consumed downstream)
# ---------------------------------------------------------------------------

@dataclass(frozen=True, slots=True)
class Upsert:
    """Insert or replace the window keyed by ``window.id``."""

    window: Window


@dataclass(frozen=True, slots=True)
class RetractWindow:
    """The window ``window_id`` no longer exists; remove it downstream."""

    window_id: int
    source: str
    start_ms: int
    end_ms: int


@dataclass(frozen=True, slots=True)
class CloseWindow:
    """The window fell fully outside the allowed-lateness horizon and was
    reclaimed.  ``window`` is the final, immutable snapshot."""

    window: Window


Change = Union[Upsert, RetractWindow, CloseWindow]


# ---------------------------------------------------------------------------
# Results
# ---------------------------------------------------------------------------

class Disposition(enum.Enum):
    """Explicit per-record outcome."""

    APPLIED = "applied"                    # event accepted, windows updated
    DUPLICATE = "duplicate"                # same (source, seq), same payload: ignored
    CONFLICT = "conflict"                  # same (source, seq), different payload: ignored
    LATE_REJECTED = "late_rejected"        # outside allowed lateness: rejected
    RETRACTED = "retracted"                # retraction accepted, windows updated
    RETRACT_UNKNOWN = "retract_unknown"    # no such (live) event: no-op


@dataclass(frozen=True, slots=True)
class ApplyResult:
    op: Operation
    disposition: Disposition
    changes: tuple[Change, ...] = ()

    @property
    def applied(self) -> bool:
        return self.disposition in (Disposition.APPLIED, Disposition.RETRACTED)


@dataclass(frozen=True, slots=True)
class BatchResult:
    results: tuple[ApplyResult, ...]

    @property
    def changes(self) -> tuple[Change, ...]:
        """All changes, in application order.  Downstream must apply them in
        this exact order to reproduce the final state."""
        return tuple(c for r in self.results for c in r.changes)
