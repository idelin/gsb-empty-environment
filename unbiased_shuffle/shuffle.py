"""Pausable in-memory Fisher-Yates shuffle.

The shuffle runs from frontier i = n-1 down to 1; at each step exactly one
sample j ~ Uniform[0, i] is drawn and positions i and j are swapped.  The
full state (items, frontier, rng state, trace) can be snapshotted at any
step and restored later, which makes the shuffle pausable and resumable.
"""

from __future__ import annotations


class PausableShuffle:
    """Fisher-Yates with an explicit swap frontier and snapshot support."""

    def __init__(self, items, rng):
        self.items = list(items)
        self.rng = rng
        self.frontier = len(self.items) - 1  # positions > frontier are final
        self.samples = 0                     # randbelow calls so far
        self.trace = []                      # (frontier, j) per swap, for audit

    @property
    def done(self) -> bool:
        return self.frontier < 1

    def step(self) -> bool:
        """Perform one swap; return False when already finished."""
        if self.done:
            return False
        i = self.frontier
        j = self.rng.randbelow(i + 1)  # uniform on [0, i]
        if not 0 <= j <= i:
            raise AssertionError(f"rng returned out-of-range index {j} for [0, {i}]")
        self.items[i], self.items[j] = self.items[j], self.items[i]
        self.trace.append((i, j))
        self.samples += 1
        self.frontier -= 1
        return True

    def run(self, max_steps: int | None = None) -> list:
        """Run up to ``max_steps`` swaps (or to completion) and return items."""
        steps = 0
        while not self.done and (max_steps is None or steps < max_steps):
            self.step()
            steps += 1
        return self.items

    def snapshot(self) -> dict:
        """Capture the complete resumable state."""
        return {
            "items": list(self.items),
            "frontier": self.frontier,
            "samples": self.samples,
            "trace": list(self.trace),
            "rng_state": self.rng.getstate(),
        }

    @classmethod
    def restore(cls, snapshot: dict, rng) -> "PausableShuffle":
        """Rebuild a shuffle from a snapshot; ``rng`` state is overwritten."""
        rng.setstate(snapshot["rng_state"])
        obj = cls.__new__(cls)
        obj.items = list(snapshot["items"])
        obj.rng = rng
        obj.frontier = snapshot["frontier"]
        obj.samples = snapshot["samples"]
        obj.trace = [tuple(t) for t in snapshot["trace"]]
        return obj


def fisher_yates(items, rng) -> list:
    """One-shot convenience wrapper around :class:`PausableShuffle`."""
    return PausableShuffle(items, rng).run()
