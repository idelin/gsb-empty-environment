"""Independent reference implementation.

Recomputes all windows from scratch on every call by sorting the full event
set.  Deliberately naive (O(n log n) per call, no incremental state, no ids):
it exists purely as an oracle to validate the incremental store.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable

from .types import Event, Member

__all__ = ["RefWindow", "compute_windows"]


@dataclass(frozen=True)
class RefWindow:
    source: str
    start_ms: int
    end_ms: int
    members: tuple[Member, ...]


def compute_windows(events: Iterable[Event], gap_ms: int) -> list[RefWindow]:
    """Sessionize ``events`` (first write wins per (source, seq))."""
    live: dict[tuple[str, int], Event] = {}
    for ev in events:
        live.setdefault((ev.source, ev.seq), ev)

    by_source: dict[str, list[Event]] = {}
    for ev in live.values():
        by_source.setdefault(ev.source, []).append(ev)

    out: list[RefWindow] = []
    for source in sorted(by_source):
        ordered = sorted(by_source[source], key=lambda e: (e.event_ms, e.seq))
        group: list[Event] = []
        for ev in ordered:
            if group and ev.event_ms - group[-1].event_ms > gap_ms:
                out.append(_window(source, group))
                group = []
            group.append(ev)
        if group:
            out.append(_window(source, group))
    return out


def _window(source: str, group: list[Event]) -> RefWindow:
    return RefWindow(
        source=source,
        start_ms=group[0].event_ms,
        end_ms=group[-1].event_ms,
        members=tuple(Member(e.seq, e.event_ms, e.value) for e in group),
    )
