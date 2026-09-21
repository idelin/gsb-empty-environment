"""Public data model for the session-window library."""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Optional, Tuple, Union


@dataclass(frozen=True)
class Event:
    """An input record. ``(source, seq)`` is the stable identity used by
    retractions; ``event_ms`` is the event-time timestamp."""
    source: str
    seq: int
    event_ms: int
    value: Any = None


@dataclass(frozen=True)
class Retract:
    """Retraction of a previously accepted event, addressed by identity."""
    source: str
    seq: int


@dataclass(frozen=True)
class AdvanceWatermark:
    """Advance the event-time watermark of one source."""
    source: str
    watermark_ms: int


Op = Union[Event, Retract, AdvanceWatermark]


def _member_key(ev: Event) -> Tuple[int, int]:
    return (ev.event_ms, ev.seq)


@dataclass(frozen=True)
class Window:
    """A session window. ``members`` is sorted by ``(event_ms, seq)``.

    ``id`` is deterministic: ``"<source>@<start_ms>"`` where ``start_ms`` is
    the smallest event time in the window. Because the id is a pure function
    of the window's content, the same final state always yields the same ids
    regardless of arrival order.
    """
    id: str
    source: str
    start_ms: int
    end_ms: int
    members: Tuple[Event, ...]
    closed: bool = False

    @staticmethod
    def make_id(source: str, start_ms: int) -> str:
        return f"{source}@{start_ms}"


class ChangeKind(Enum):
    #: The window with this id no longer exists with its old content;
    #: consumers must delete it. Emitted when windows are merged away or a
    #: window becomes empty.
    RETRACT = "retract"
    #: Insert or replace the window with this id.
    UPSERT = "upsert"
    #: Final upsert: the watermark passed the window, it can never change
    #: again and its state has been reclaimed by the engine.
    CLOSE = "close"


@dataclass(frozen=True)
class Change:
    kind: ChangeKind
    window_id: str
    #: Set for UPSERT and CLOSE, None for RETRACT.
    window: Optional[Window] = None


class Disposition(Enum):
    #: Event accepted; ``changes`` describes the resulting window updates.
    ACCEPTED = "accepted"
    #: An event with the same ``(source, seq)`` was already accepted;
    #: the redelivery is ignored (first write wins).
    DUPLICATE = "duplicate"
    #: ``event_ms`` is older than ``watermark - allowed_lateness``; the event
    #: is dropped and never affects any window.
    DROPPED_LATE = "dropped_late"
    #: Retraction applied; ``changes`` describes the resulting window updates.
    RETRACTED = "retracted"
    #: Retraction referenced an unknown ``(source, seq)`` — either never seen,
    #: already retracted, or already reclaimed by the watermark. No-op.
    RETRACT_UNKNOWN = "retract_unknown"
    #: Watermark advanced (possibly a no-op if it regressed).
    WATERMARK = "watermark"


@dataclass
class IndexStats:
    """Which per-source indexes a revision touched, and how much work it did.
    Counters exist so callers (and tests) can confirm that no revision ever
    scans the full retained history."""
    #: Sessions merged into the surviving window (insert path).
    sessions_merged: int = 0
    #: Segments produced by re-segmenting one window (retract path).
    segments_produced: int = 0
    #: Member events re-sorted/re-segmented (bounded by one window's size).
    members_touched: int = 0
    #: Sessions reclaimed by a watermark advance.
    sessions_reclaimed: int = 0
    #: Events purged from the identity index by reclamation.
    events_reclaimed: int = 0


@dataclass
class Revision:
    """The result of applying one op."""
    rev: int
    op: Op
    disposition: Disposition
    #: Changelog for downstream consumers. RETRACTs always precede UPSERTs
    #: within one revision.
    changes: Tuple[Change, ...] = ()
    stats: IndexStats = field(default_factory=IndexStats)


@dataclass
class BatchResult:
    revisions: Tuple[Revision, ...]
    #: Concatenation of every revision's changelog, in order.
    changes: Tuple[Change, ...]
