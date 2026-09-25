"""Control-event definitions for the unified audio/control scheduler."""
from __future__ import annotations

from dataclasses import dataclass

GAIN = "gain"
ROUTE = "route"
FILTER = "filter"
KINDS = (GAIN, ROUTE, FILTER)


@dataclass(frozen=True)
class ControlEvent:
    """A control change bound to the sample clock.

    - ``epoch``: sample-clock epoch. Events whose epoch does not match the
      scheduler's current epoch are dropped and never fire.
    - ``position``: absolute sample index at which the event takes effect
      (the event applies to samples ``>= position``).
    - ``version``: monotonic version for late corrections; for a given
      ``event_id`` only the highest version is kept.
    - ``kind``: one of ``gain`` / ``route`` / ``filter``.
    - ``params``:
        - gain:   ``{"track": name, "gain": float}``
        - filter: ``{"track": name, "cutoff": Hz}``  (one-pole low-pass)
        - route:  ``{"op": "connect"|"disconnect", "src": node, "dst": node}``
    """

    event_id: str
    epoch: int
    position: int
    version: int
    kind: str
    params: dict
