"""Deterministic RNG wrapper with snapshot/restore for exact replay."""
from __future__ import annotations

import random


class Rng:
    """Wraps random.Random so stochastic ops can be replayed bit-exactly.

    The engine snapshots the state before every node during the forward
    pass; a recompute of any segment restores the snapshot taken before
    the segment's first node, so stochastic ops draw identical values.
    """

    def __init__(self, seed):
        self.seed = seed
        self._r = random.Random(seed)

    def snapshot(self):
        return self._r.getstate()

    def restore(self, state):
        self._r.setstate(state)

    def gauss(self, mu=0.0, sigma=1.0):
        return self._r.gauss(mu, sigma)

    def random(self):
        return self._r.random()
