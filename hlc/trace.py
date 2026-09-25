"""Observable trace records.

The trace is the *only* thing the cross-node validator consumes: a flat,
append-only stream of facts (what happened on which node, with which tag).
The validator never inspects node internals, which is what makes it an
independent check.
"""
from __future__ import annotations

from dataclasses import dataclass, field, asdict
from typing import Any, Optional

from .tag import Tag


@dataclass
class EventRecord:
    node: str
    kind: str  # local|send|receive|duplicate|quarantine|crash|restart
    seq: int = -1
    tag: Optional[Tag] = None
    msg_id: Optional[str] = None
    peer: Optional[str] = None
    attempts: int = 1
    wall: Optional[int] = None
    detail: str = ""
    payload: Optional[str] = None

    def to_dict(self) -> dict[str, Any]:
        data = asdict(self)
        data["tag"] = self.tag.to_dict() if self.tag is not None else None
        return data


class TraceCollector:
    def __init__(self) -> None:
        self._events: list[EventRecord] = []

    def add(self, event: EventRecord) -> EventRecord:
        event.seq = len(self._events)
        self._events.append(event)
        return event

    @property
    def events(self) -> list[EventRecord]:
        return self._events

    def to_list(self) -> list[dict[str, Any]]:
        return [event.to_dict() for event in self._events]
