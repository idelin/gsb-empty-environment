from __future__ import annotations

from typing import Iterable, Set, Tuple

from ab_pattern import Event


def reference_matches(
    events: Iterable[Event],
    retracted_a: Set[Tuple[str, int]],
    gap_ms: int = 5,
) -> Set[Tuple[str, Tuple[str, int], Tuple[str, int]]]:
    ordered = sorted(events, key=lambda event: event.order_key)
    a_events = [
        event
        for event in ordered
        if event.kind == "A" and event.event_id not in retracted_a
    ]
    b_events = [event for event in ordered if event.kind == "B"]
    c_events = [event for event in ordered if event.kind == "C"]

    matches = set()
    for a_event in a_events:
        for b_event in b_events:
            if not a_event.order_key < b_event.order_key:
                continue
            if b_event.ts > a_event.ts + gap_ms:
                continue
            blocked = any(
                a_event.order_key < c_event.order_key < b_event.order_key
                for c_event in c_events
            )
            if not blocked:
                matches.add((a_event.key, a_event.event_id, b_event.event_id))
    return matches
