"""Deterministic delivery-stream generator.

Given the logical events from a local fixture, produces a *delivery* stream
containing transactions, duplicates, out-of-order deliveries and crash
points.  Everything is driven by one seeded ``random.Random``, so a seed
fully determines the stream.

It can also produce deliberately *invalid* streams (partial transactions,
broken before/after chains, conflicting duplicates) to exercise the
validator's invalid-stream path, kept separate from processing errors.
"""

from __future__ import annotations

import random
from dataclasses import replace

from .model import CRASH_MARKER, ChangeEvent, CrashPoint, DeliveryItem


class StreamGenerator:
    def __init__(self, seed: int):
        self.seed = seed
        self.rng = random.Random(seed)

    def _windowed_shuffle(self, events: list[ChangeEvent], window: int) -> list[ChangeEvent]:
        """Shuffle within sliding windows: bounded out-of-order delivery."""
        out: list[ChangeEvent] = []
        for start in range(0, len(events), window):
            chunk = events[start : start + window]
            self.rng.shuffle(chunk)
            out.extend(chunk)
        return out

    def _inject_duplicates(
        self, items: list[ChangeEvent], probability: float
    ) -> list[ChangeEvent]:
        """Duplicate some events at a random later position (redelivery)."""
        out = list(items)
        for ev in list(items):
            if self.rng.random() < probability:
                pos = self.rng.randrange(len(out) + 1)
                out.insert(pos, ev)
        return out

    def generate(
        self,
        events: list[ChangeEvent],
        *,
        reorder_window: int = 3,
        duplicate_probability: float = 0.25,
        crashes: int = 1,
    ) -> list[DeliveryItem]:
        """Build a deterministic delivery stream with dup/reorder/crashes."""
        base = sorted(events, key=lambda e: e.commit_seq)
        delivered = self._inject_duplicates(
            self._windowed_shuffle(base, max(1, reorder_window)),
            duplicate_probability,
        )
        stream: list[DeliveryItem] = list(delivered)
        for _ in range(crashes):
            # Crash somewhere in the back half, then redelivery of everything
            # delivered so far (re-shuffled, as a restart would replay).
            cut = self.rng.randrange(max(1, len(stream) // 2), len(stream) + 1)
            so_far = [it for it in stream[:cut] if isinstance(it, ChangeEvent)]
            replay = self._windowed_shuffle(so_far, max(1, reorder_window))
            stream = stream[:cut] + [CrashPoint()] + replay + stream[cut:]
        return stream

    def generate_invalid(
        self, events: list[ChangeEvent], kind: str = "partial_tx"
    ) -> list[DeliveryItem]:
        """Build a deterministic *invalid* stream for validator testing."""
        base: list[DeliveryItem] = sorted(events, key=lambda e: e.commit_seq)
        if kind == "partial_tx":
            # Drop one event of a multi-event transaction: tx_size no longer
            # matches the delivered event count.
            victim = next(e for e in base if isinstance(e, ChangeEvent) and e.tx_size > 1)
            base.remove(victim)
        elif kind == "broken_chain":
            # Corrupt a 'before' value so the per-key before/after chain
            # no longer lines up.
            for i, item in enumerate(base):
                if isinstance(item, ChangeEvent) and item.before is not None:
                    base[i] = replace(item, before={"corrupted": True})
                    break
        elif kind == "conflicting_dup":
            # Re-deliver an event id with different content.
            first = next(e for e in base if isinstance(e, ChangeEvent))
            base.append(replace(first, after={"tampered": True}))
        else:
            raise ValueError(f"unknown invalid kind: {kind}")
        return base


__all__ = ["StreamGenerator", "CRASH_MARKER"]
