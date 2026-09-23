"""Core data model for CDC (change data capture) test streams.

A *logical* stream is a list of transactions; each transaction is a list of
ChangeEvents sharing a ``tx_id``.  A *delivery* stream is a list of
DeliveryItems (ChangeEvent or CrashPoint) as the processor actually sees
them: possibly duplicated, out of order, and interrupted by crashes.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Union

CRASH_MARKER = "CRASH"


@dataclass(frozen=True)
class ChangeEvent:
    """One row-level change.

    ``before is None`` means insert, ``after is None`` means delete.
    ``tx_size`` is the declared number of events in the transaction, so a
    stream is self-describing and completeness can be checked from the
    stream alone (no external catalog needed).
    """

    event_id: int
    tx_id: int
    table: str
    pk: str
    before: Any  # None => row did not exist
    after: Any   # None => row deleted
    commit_seq: int
    tx_size: int = 1

    @property
    def key(self) -> tuple[str, str]:
        return (self.table, self.pk)


@dataclass(frozen=True)
class CrashPoint:
    """Delivery-stream marker: the processor crashes and restarts here.

    Volatile state (dedupe caches, buffers) is lost; durable materialized
    state survives.  Events after the marker may be redeliveries.
    """

    marker: str = CRASH_MARKER


DeliveryItem = Union[ChangeEvent, CrashPoint]


def events_of(items: list[DeliveryItem]) -> list[ChangeEvent]:
    """Distinct events of a delivery stream, in first-delivery order."""
    seen: dict[int, ChangeEvent] = {}
    for item in items:
        if isinstance(item, ChangeEvent) and item.event_id not in seen:
            seen[item.event_id] = item
    return list(seen.values())


def format_stream(items: list[DeliveryItem]) -> str:
    """Human-readable rendering of a delivery stream."""
    lines = []
    for item in items:
        if isinstance(item, CrashPoint):
            lines.append("  --- CRASH ---")
        else:
            lines.append(
                f"  ev#{item.event_id} tx={item.tx_id} seq={item.commit_seq} "
                f"{item.table}/{item.pk} {item.before!r} -> {item.after!r}"
            )
    return "\n".join(lines)
