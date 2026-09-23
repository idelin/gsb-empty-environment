"""Upstream: turns local change events into corrections and publishes them.

Events are ordered by commit sequence number before being folded into a
delta, so a retraction followed by a re-add of the same key is always applied
in the right order even if the fixture arrives shuffled.
"""
from __future__ import annotations

from .models import GENESIS_VERSION, ChangeEvent, Correction
from .publisher import Publisher


class Upstream:
    def __init__(self, source: str, publisher: Publisher):
        self.source = source
        self.publisher = publisher
        self.known_heads: tuple[str, ...] = (GENESIS_VERSION,)
        self._counter = 0

    def refresh(self) -> None:
        """See the publisher's current heads (enables DAG merges)."""
        self.known_heads = tuple(self.publisher.heads())

    def publish_batch(self, events: list[ChangeEvent]) -> Correction:
        if not events:
            raise ValueError("empty batch")
        for event in events:
            if event.source != self.source:
                raise ValueError(f"event from foreign source {event.source}")
        ordered = sorted(events, key=lambda e: e.commit_seq)
        deltas: dict[str, int] = {}
        for event in ordered:
            deltas[event.table] = deltas.get(event.table, 0) + event.delta
        self._counter += 1
        correction = Correction(
            revision_id=f"{self.source}-r{self._counter}",
            source=self.source,
            parents=self.known_heads,
            deltas=deltas,
            checkpoints={self.source: max(e.commit_seq for e in ordered)},
            txn_ids=tuple(e.txn_id for e in ordered),
        )
        self.publisher.publish(correction)
        return correction
