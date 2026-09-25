"""Event model."""

from __future__ import annotations

from dataclasses import dataclass, field

from .timevals import Duration, WallClock


@dataclass(frozen=True)
class Event:
    id: str
    source: str
    seq: int
    clock: WallClock
    error: Duration
    payload: str
    causal_after: tuple = field(default_factory=tuple)

    def __post_init__(self):
        object.__setattr__(self, "causal_after", tuple(self.causal_after))
