"""Core data model for the session-window changelog library.

Window identity
---------------
A window's ``window_id`` is derived from its content, not from arrival
order: it is ``"{source}:{first_event_ms}:{first_seq}"`` where
``(first_event_ms, first_seq)`` is the smallest member key of the window.

This makes id continuity deterministic and unambiguous for consumers:

* Merge: the merged window keeps the id of whichever input window (or the
  triggering event) owns the overall earliest member.  Ids of absorbed
  windows are RETRACTed.
* Split: the leftmost part keeps the original id (its first member is
  unchanged); every other part gets a fresh id derived from its own first
  member.  If the removed event was the first member, the old id dies
  (RETRACT) and all parts get fresh ids.
* Same final event set => same ids and same membership, regardless of the
  arrival order of upserts/retractions.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Iterable, Optional, Tuple, Union

__all__ = [
    "Event",
    "Member",
    "Window",
    "make_window",
    "ChangeKind",
    "Change",
    "Disposition",
    "Upsert",
    "Retract",
    "AdvanceWatermark",
    "Op",
    "OpResult",
    "BatchResult",
]


@dataclass(frozen=True)
class Event:
    """An upstream record. ``(source, seq)`` is its stable identity."""

    source: str
    seq: int
    event_ms: int
    value: Any = None


@dataclass(frozen=True)
class Member:
    """An event as stored inside a window snapshot."""

    event_ms: int
    seq: int
    value: Any = None


@dataclass(frozen=True)
class Window:
    """An immutable snapshot of one session window."""

    source: str
    window_id: str
    start_ms: int
    end_ms: int
    members: Tuple[Member, ...]

    @property
    def count(self) -> int:
        return len(self.members)


def make_window(source: str, members: Iterable[Member]) -> Window:
    """Build a window snapshot from members, deriving id and extents."""
    ordered = tuple(sorted(members, key=lambda m: (m.event_ms, m.seq)))
    if not ordered:
        raise ValueError("a window needs at least one member")
    first = ordered[0]
    return Window(
        source=source,
        window_id=f"{source}:{first.event_ms}:{first.seq}",
        start_ms=ordered[0].event_ms,
        end_ms=ordered[-1].event_ms,
        members=ordered,
    )


class ChangeKind(Enum):
    #: The window now exists with exactly this content (insert or replace).
    UPSERT = "upsert"
    #: The window id is gone; carries the last snapshot so downstream can
    #: delete without having stored anything.
    RETRACT = "retract"
    #: The watermark horizon passed the window; it is immutable and its
    #: state has been reclaimed.  Carries the final snapshot.
    FINALIZE = "finalize"


@dataclass(frozen=True)
class Change:
    kind: ChangeKind
    window: Window


class Disposition(Enum):
    #: Upsert/retract applied; ``OpResult.changes`` describes the delta.
    APPLIED = "applied"
    #: Same ``(source, seq)`` re-delivered with a different payload; the
    #: old record was replaced.
    UPDATED = "updated"
    #: Exact re-delivery of a known event; ignored, no changes.
    DUPLICATE = "duplicate"
    #: Event fell outside ``watermark - allowed_lateness``; rejected.
    TOO_LATE = "too_late"
    #: Retraction targeted an unknown (or already reclaimed) event.
    NOT_FOUND = "not_found"
    #: Watermark moved forward; changes may contain FINALIZE entries.
    WATERMARK_ADVANCED = "watermark_advanced"
    #: Watermark did not move (regression or equal); ignored.
    WATERMARK_IGNORED = "watermark_ignored"


@dataclass(frozen=True)
class Upsert:
    event: Event


@dataclass(frozen=True)
class Retract:
    source: str
    seq: int


@dataclass(frozen=True)
class AdvanceWatermark:
    source: str
    watermark_ms: int


Op = Union[Upsert, Retract, AdvanceWatermark]


@dataclass(frozen=True)
class OpResult:
    disposition: Disposition
    #: Net delta for this op: at most one entry per window id, sorted by
    #: window id, so it can be diffed/compared deterministically.
    changes: Tuple[Change, ...] = ()


@dataclass(frozen=True)
class BatchResult:
    results: Tuple[OpResult, ...]
    #: Concatenation of every op's changes, in input order.
    changes: Tuple[Change, ...]
