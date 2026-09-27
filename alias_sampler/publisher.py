"""Dual-version table publishing with replayable sampling sessions.

``AliasTablePublisher`` owns the *current* table.  ``publish_update`` builds
a brand-new table and swaps the reference; sessions opened earlier keep
their own reference to the previous table object, so in-flight sampling
sessions continue to read the old distribution untouched.

Each session records its seed and the number of draws made, which makes any
session exactly replayable from ``(table, seed, draws)``.
"""

from __future__ import annotations

import random

from .exact_alias import AliasTable


class SamplingSession:
    """A sampling session pinned to one immutable table version."""

    def __init__(self, table: AliasTable, version: int, seed, rng=None):
        self.table = table
        self.version = version
        self.seed = seed
        self._rng = rng if rng is not None else random.Random(seed)
        self.draws = 0

    def sample(self, k: int | None = None):
        if k is None:
            self.draws += 1
            return self.table.sample(self._rng)
        return [self.sample() for _ in range(k)]

    def replay(self):
        """Re-draw ``self.draws`` samples from a fresh ``Random(self.seed)``.

        Deterministic: the table is exact/immutable and the RNG is fully
        determined by the seed, so the returned sequence equals the one this
        session produced.
        """
        rng = random.Random(self.seed)
        return [self.table.sample(rng) for _ in range(self.draws)]

    def state_dict(self) -> dict:
        return {"version": self.version, "seed": self.seed, "draws": self.draws}


class AliasTablePublisher:
    """Publishes alias-table versions; old sessions keep old distributions."""

    def __init__(self, weights):
        self._table = AliasTable(weights)
        self._version = 0

    @property
    def version(self) -> int:
        return self._version

    def current_table(self) -> AliasTable:
        return self._table

    def publish_update(self, weights) -> int:
        """Build a new table from `weights` and publish it atomically."""
        new_table = AliasTable(weights)
        self._table = new_table
        self._version += 1
        return self._version

    def open_session(self, seed) -> SamplingSession:
        return SamplingSession(self._table, self._version, seed)
