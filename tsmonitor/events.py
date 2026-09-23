"""Event model for the three-valued online monitor."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Event:
    """A single occurrence in the event stream.

    ``id`` is the unique identity used for de-duplication and retraction;
    ``ts`` is the event time (which may arrive out of order); ``symbol``
    must belong to the explicitly declared monitor alphabet.
    """

    id: str
    ts: float
    symbol: str
