"""Event records and duplicate-ID handling."""
from __future__ import annotations

from dataclasses import dataclass

from .timemodel import WallClock


class ConflictError(Exception):
    """Raised when the same event id appears with conflicting content."""


@dataclass(frozen=True, slots=True)
class Event:
    """One log record.

    ``causes`` lists ids of events that must have happened before this one
    (explicit cross-source or in-source causality, e.g. a request id).
    The per-source local order is given implicitly by the order in which
    events of a source appear in the input sequence.
    """

    id: str
    source: str
    wall: WallClock
    payload: str = ""
    causes: tuple[str, ...] = ()


def dedupe(events) -> list[Event]:
    """Validate the event stream.

    * identical re-transmissions of the same id are collapsed;
    * the same id with different content is rejected outright.
    """
    seen: dict[str, Event] = {}
    out: list[Event] = []
    for e in events:
        prev = seen.get(e.id)
        if prev is not None:
            if prev != e:
                raise ConflictError(
                    f"event id {e.id!r} arrived with conflicting content: "
                    f"{prev!r} vs {e!r}"
                )
            continue
        seen[e.id] = e
        out.append(e)
    return out
