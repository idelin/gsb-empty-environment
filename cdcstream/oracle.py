"""Independent full-state oracle.

Computes the expected final state from the *logical* event set in one bulk
pass: for each key, keep the event with the highest commit_seq.  It never
touches the incremental processor or its algorithm — no shared code, no
shared state — so it can serve as an independent oracle for validation.
"""

from __future__ import annotations

from typing import Any, Iterable

from .model import ChangeEvent


def full_state(events: Iterable[ChangeEvent]) -> dict[tuple[str, str], Any]:
    """Final state = per key, the 'after' of the max-commit_seq event."""
    latest: dict[tuple[str, str], ChangeEvent] = {}
    for ev in events:
        current = latest.get(ev.key)
        if current is None or ev.commit_seq > current.commit_seq:
            latest[ev.key] = ev
    return {key: ev.after for key, ev in latest.items() if ev.after is not None}
