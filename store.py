"""Collision-safe canonical-state store with a recovery mapping.

The store deduplicates *canonical* states produced by the model's
``canonicalize`` function (which may compress/abstract concrete states).
For every canonical form it keeps the first concrete representative so
that counterexample traces remain replayable on the original model.

Hash collisions are resolved by comparing full canonical states with
``==``; the hash is only used to bucket entries.
"""
from __future__ import annotations

from typing import Any, Hashable


class StateStore:
    def __init__(self) -> None:
        self._buckets: dict[int, list[Hashable]] = {}
        self.recovery: dict[Hashable, Any] = {}
        self.collisions = 0

    def add_if_new(self, canonical: Hashable, concrete: Any) -> bool:
        """Insert ``canonical`` if unseen. Returns True iff it was new.

        On a hash hit the full canonical state is compared, so distinct
        states that merely share a hash are both kept.
        """
        h = hash(canonical)
        bucket = self._buckets.get(h)
        if bucket is None:
            self._buckets[h] = [canonical]
            self.recovery[canonical] = concrete
            return True
        for existing in bucket:
            if existing == canonical:
                return False
            self.collisions += 1  # same hash, different full state
        bucket.append(canonical)
        self.recovery[canonical] = concrete
        return True

    def __contains__(self, canonical: Hashable) -> bool:
        bucket = self._buckets.get(hash(canonical))
        return bucket is not None and any(e == canonical for e in bucket)

    def __len__(self) -> int:
        return len(self.recovery)

    def canonicals(self) -> list[Hashable]:
        return list(self.recovery.keys())

    def snapshot(self):
        return (self._buckets, self.recovery, self.collisions)

    def restore(self, snapshot) -> None:
        self._buckets, self.recovery, self.collisions = snapshot
