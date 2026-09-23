"""Automatic shrinking of failing delivery streams.

Given a stream that makes the validator fail (mismatch or processing
error), greedily remove deliveries while the failure persists.  Reduction
is transaction-structure aware: events are removed only as whole
transactions, never individually, so the shrunk stream stays replayable.
Crash points and duplicate deliveries are removable units of their own.

Every candidate is re-checked through ``run_and_validate``; candidates
that become invalid streams (e.g. partial transactions, broken chains)
or stop failing are rejected, so the result is always a well-formed,
replayable, still-failing stream.
"""

from __future__ import annotations

from typing import Callable, Hashable, Iterable

from .model import ChangeEvent, CrashPoint, DeliveryItem
from .validator import run_and_validate


def _ddmin(units: list[Hashable], keeps_failing: Callable[[frozenset], bool]) -> frozenset:
    """Classic delta-debugging: find a (near-)minimal removable subset.

    ``keeps_failing(removed)`` must return True when removing ``removed``
    from the stream preserves the failure.
    """
    removed: set[Hashable] = set()
    remaining = list(units)
    granularity = 2
    while remaining:
        chunk = max(1, len(remaining) // granularity)
        progressed = False
        for start in range(0, len(remaining), chunk):
            group = remaining[start : start + chunk]
            candidate = removed | set(group)
            if keeps_failing(frozenset(candidate)):
                removed = set(candidate)
                remaining = [u for u in remaining if u not in group]
                granularity = max(2, granularity - 1)
                progressed = True
                break
        if not progressed:
            if granularity >= len(remaining):
                break
            granularity = min(len(remaining), granularity * 2)
    return frozenset(removed)


def shrink(
    items: list[DeliveryItem],
    processor_factory,
    *,
    max_rounds: int = 10,
) -> list[DeliveryItem]:
    """Reduce a failing stream; result stays well-formed and replayable."""

    def still_fails(candidate: list[DeliveryItem]) -> bool:
        if not any(isinstance(it, ChangeEvent) for it in candidate):
            return False
        return run_and_validate(candidate, processor_factory).failed

    if not still_fails(items):
        raise ValueError("stream does not fail; nothing to shrink")

    current = list(items)
    for _ in range(max_rounds):
        before = len(current)

        # Phase 1: drop whole transactions (all their deliveries at once).
        tx_ids = sorted({it.tx_id for it in current if isinstance(it, ChangeEvent)})

        def without_txs(removed: frozenset) -> list[DeliveryItem]:
            return [
                it
                for it in current
                if not (isinstance(it, ChangeEvent) and it.tx_id in removed)
            ]

        drop_txs = _ddmin(tx_ids, lambda rm: still_fails(without_txs(rm)))
        current = without_txs(drop_txs)

        # Phase 2: drop crash points (restart markers).
        crash_idx = [i for i, it in enumerate(current) if isinstance(it, CrashPoint)]

        def without_crashes(removed: frozenset) -> list[DeliveryItem]:
            return [it for i, it in enumerate(current) if i not in removed]

        drop_crashes = _ddmin(crash_idx, lambda rm: still_fails(without_crashes(rm)))
        current = without_crashes(drop_crashes)

        # Phase 3: drop duplicate deliveries (2nd+ occurrence of an event).
        seen: set[int] = set()
        dup_idx = []
        for i, it in enumerate(current):
            if isinstance(it, ChangeEvent):
                if it.event_id in seen:
                    dup_idx.append(i)
                else:
                    seen.add(it.event_id)

        def without_dups(removed: frozenset) -> list[DeliveryItem]:
            return [it for i, it in enumerate(current) if i not in removed]

        drop_dups = _ddmin(dup_idx, lambda rm: still_fails(without_dups(rm)))
        current = without_dups(drop_dups)

        if len(current) == before:
            break  # fixpoint: no phase could remove anything further
    return current
