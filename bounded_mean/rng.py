"""Caller-injected randomness with provenance recording.

Estimators never create their own random source: the caller injects one.
:class:`RecordingRandom` wraps ``random.Random`` (or any compatible
object), records the seed and counts how many random draws were consumed,
so every evidence file can state exactly which random source and how much
of it was used.
"""
from __future__ import annotations

import random

__all__ = ["RecordingRandom"]


class RecordingRandom:
    """Wrap a random source, recording the seed and the number of draws."""

    def __init__(self, seed=None, rng: random.Random | None = None):
        if rng is not None and seed is not None:
            raise ValueError("pass either seed or rng, not both")
        self.seed = seed if rng is None else None
        self._rng = rng if rng is not None else random.Random(seed)
        self.draws = 0

    def random(self) -> float:
        self.draws += 1
        return self._rng.random()

    def __getattr__(self, name):
        # Delegate any other random.Random API (randrange, choice, ...).
        return getattr(self._rng, name)

    def provenance(self) -> dict:
        return {"seed": self.seed, "rng_draws": self.draws}
