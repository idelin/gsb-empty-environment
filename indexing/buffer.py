"""Out-of-order sequence buffer with gap (missing seq) reporting."""
from __future__ import annotations

from .events import Event


class SeqBuffer:
    """Holds events that arrived ahead of their predecessors.

    ``next_expected`` is the smallest seq not yet released. ``push`` either
    releases a contiguous run of events (in seq order), buffers the event,
    or rejects it as a duplicate.
    """

    def __init__(self, next_expected: int = 1, pending: dict | None = None):
        self.next_expected = next_expected
        self.pending: dict[int, Event] = dict(pending or {})
        self.duplicate_count = 0

    def push(self, event: Event) -> tuple[str, list]:
        """Accept an event. Returns (status, drained events in seq order).

        status is one of:
          "duplicate" - seq already released or already buffered; ignored.
          "buffered"  - seq is ahead of a gap; held until the gap fills.
          "drained"   - event filled the head gap; one or more contiguous
                        events are released in ascending seq order.
        """
        if event.seq < self.next_expected or event.seq in self.pending:
            self.duplicate_count += 1
            return "duplicate", []
        self.pending[event.seq] = event
        drained = self._drain()
        return ("drained" if drained else "buffered"), drained

    def _drain(self) -> list:
        drained: list[Event] = []
        while self.next_expected in self.pending:
            drained.append(self.pending.pop(self.next_expected))
            self.next_expected += 1
        return drained

    def missing_ranges(self) -> list:
        """Explicit report of missing seqs as inclusive (lo, hi) ranges."""
        if not self.pending:
            return []
        hi = max(self.pending)
        ranges: list[tuple[int, int]] = []
        start = None
        for seq in range(self.next_expected, hi + 1):
            if seq not in self.pending:
                if start is None:
                    start = seq
            elif start is not None:
                ranges.append((start, seq - 1))
                start = None
        if start is not None:
            ranges.append((start, hi))
        return ranges

    def to_dict(self) -> dict:
        return {
            "next_expected": self.next_expected,
            "pending": [self.pending[s].to_dict() for s in sorted(self.pending)],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "SeqBuffer":
        pending = {e["seq"]: Event.from_dict(e) for e in data["pending"]}
        return cls(next_expected=data["next_expected"], pending=pending)
