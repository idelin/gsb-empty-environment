"""Control event definitions for the unified audio/control scheduler.

Every control event carries a sample-clock ``epoch`` and a monotonic
``version`` so that late corrections can supersede earlier submissions and
stale submissions can be discarded deterministically.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any


class EventKind(Enum):
    SET_GAIN = "set_gain"
    SET_ROUTE = "set_route"
    SET_FILTER = "set_filter"


@dataclass(frozen=True)
class ControlEvent:
    """A sample-accurate control event.

    Attributes:
        event_id: Stable identity; corrections reuse the id with a higher
            ``version``.
        epoch: Sample-clock epoch the event belongs to.  Events whose epoch
            does not match the engine's current epoch are quarantined and
            never fire.
        position: Absolute sample index (within ``epoch``) at which the
            event takes effect.
        version: Monotonic version; higher versions replace pending or
            unconfirmed lower versions of the same ``event_id``.
        kind: One of :class:`EventKind`.
        target: Node name for gain/filter events (informational for route
            events, which address an edge via ``params``).
        params: Kind-specific payload:
            - SET_GAIN:   {"gain": float}            (finite, >= 0)
            - SET_FILTER: {"cutoff": float | None}   (None = bypass,
                          else 0 < cutoff < sample_rate / 2)
            - SET_ROUTE:  {"action": "connect"|"disconnect",
                           "src": str, "dst": str}
    """

    event_id: str
    epoch: int
    position: int
    version: int
    kind: EventKind
    target: str
    params: dict[str, Any] = field(default_factory=dict)


def gain_event(event_id: str, epoch: int, position: int, version: int,
               target: str, gain: float) -> ControlEvent:
    return ControlEvent(event_id, epoch, position, version,
                        EventKind.SET_GAIN, target, {"gain": gain})


def filter_event(event_id: str, epoch: int, position: int, version: int,
                 target: str, cutoff: float | None) -> ControlEvent:
    return ControlEvent(event_id, epoch, position, version,
                        EventKind.SET_FILTER, target, {"cutoff": cutoff})


def route_event(event_id: str, epoch: int, position: int, version: int,
                action: str, src: str, dst: str) -> ControlEvent:
    return ControlEvent(event_id, epoch, position, version,
                        EventKind.SET_ROUTE, src,
                        {"action": action, "src": src, "dst": dst})
