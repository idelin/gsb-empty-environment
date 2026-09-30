"""Audit trail events produced by the solver and checked by the replayer."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any

DECIDE = "DECIDE"        # assign a variable to a value (search branching)
PROPAGATE = "PROPAGATE"  # remove a domain value, justified by a constraint
CONFLICT = "CONFLICT"    # a domain became empty under the current state
BACKTRACK = "BACKTRACK"  # undo a decision, restore snapshot, drop tried value

KINDS = (DECIDE, PROPAGATE, CONFLICT, BACKTRACK)


@dataclass(frozen=True)
class Event:
    kind: str
    var: str | None = None
    value: Any = None
    cid: str | None = None
    level: int = 0

    def __str__(self) -> str:
        parts = [self.kind]
        if self.var is not None:
            parts.append(f"var={self.var}")
        if self.value is not None:
            parts.append(f"value={self.value}")
        if self.cid is not None:
            parts.append(f"cid={self.cid}")
        parts.append(f"level={self.level}")
        return " ".join(parts)


class Trail:
    def __init__(self) -> None:
        self.events: list[Event] = []

    def record(self, event: Event) -> None:
        self.events.append(event)

    def __len__(self) -> int:
        return len(self.events)

    def __iter__(self):
        return iter(self.events)
