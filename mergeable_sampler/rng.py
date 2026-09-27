"""Caller-injected random sources."""

from __future__ import annotations

import random
from typing import Callable, Iterable

__all__ = ["RecordedRandom", "ScriptedRandom"]


class RecordedRandom:
    """Wraps a seeded RNG and records the seed and the number of draws.

    Inject into ``Shard`` to make runs reproducible and auditable.
    """

    def __init__(self, seed, rng_factory: Callable = random.Random) -> None:
        self.seed = seed
        self._rng = rng_factory(seed)
        self.draws = 0

    def random(self) -> float:
        self.draws += 1
        return self._rng.random()


class ScriptedRandom:
    """Plays back a fixed key sequence; used to inject exact keys in tests."""

    def __init__(self, keys: Iterable[float]) -> None:
        self._keys = iter(keys)
        self.draws = 0

    def random(self) -> float:
        self.draws += 1
        return next(self._keys)
