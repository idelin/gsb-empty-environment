"""Exact weighted-quantile store.

Layered on :class:`WeightedTreap`, this store tracks individual
observations by ID so that updates and retractions keep the total weight
consistent:

* ``upsert`` with an already-known ID and an identical payload is an
  idempotent no-op; a different payload atomically replaces the old
  observation.
* zero-weight observations are eliminated (never kept in the tree);
  negative weights are rejected.
* observations can be retracted individually, by subject, or in bulk by
  sliding-window expiry.
* independent shards can be merged; identical duplicate IDs merge
  idempotently, conflicting duplicates raise.

All weights are exact rationals (``fractions.Fraction``).  Results derived
from the full population are labelled ``EXACT``; sampling-based estimates
produced by the fuzz harness are labelled ``EMPIRICAL``.
"""

from __future__ import annotations

import heapq
import itertools
import random
import zlib
from dataclasses import dataclass
from fractions import Fraction
from typing import Any, Dict, Iterator, List, Optional, Tuple

from .treap import WeightedTreap

EXACT = "exact"
EMPIRICAL = "empirical"


@dataclass(frozen=True)
class QuantileResult:
    """Weighted quantile answer: the value interval [low, high]."""

    low: Any
    high: Any
    label: str = EXACT

    @property
    def is_interval(self) -> bool:
        """True when q hit a discrete jump boundary and any value in
        [low, high] is a valid quantile."""
        return self.low != self.high


@dataclass(frozen=True)
class _Record:
    value: Any
    weight: Fraction
    subject: Any
    timestamp: Any


class WeightedStore:
    """Observation store with exact weighted quantile and ECDF queries."""

    def __init__(self, rng: Optional[random.Random] = None, window: Any = None) -> None:
        self._rng = rng if rng is not None else random.Random()
        self._tree = WeightedTreap(self._rng)
        self._records: Dict[Any, _Record] = {}
        self._subjects: Dict[Any, set] = {}
        self._window = window  # sliding-window length, or None
        self._expiry: List[Tuple[Any, int, Any]] = []  # (timestamp, seq, obs_id)
        self._seq = itertools.count()

    # ------------------------------------------------------------------
    # validation
    # ------------------------------------------------------------------
    @staticmethod
    def _checked_weight(weight: Any) -> Fraction:
        w = Fraction(weight)
        if w < 0:
            raise ValueError("negative weights are rejected")
        return w

    # ------------------------------------------------------------------
    # mutation
    # ------------------------------------------------------------------
    def upsert(self, obs_id: Any, value: Any, weight: Any,
               subject: Any = None, timestamp: Any = None) -> bool:
        """Insert or replace an observation.  Returns True if state changed.

        Re-applying an identical (id, value, weight, subject, timestamp)
        tuple is an idempotent no-op.
        """
        w = self._checked_weight(weight)
        rec = _Record(value, w, subject, timestamp)
        old = self._records.get(obs_id)
        if old == rec:
            return False
        if old is not None:
            self._remove(obs_id, old)
        if w == 0:
            return old is not None  # zero weight: observation stays eliminated
        self._records[obs_id] = rec
        self._tree.add(value, w)
        if subject is not None:
            self._subjects.setdefault(subject, set()).add(obs_id)
        if timestamp is not None:
            heapq.heappush(self._expiry, (timestamp, next(self._seq), obs_id))
        return True

    def _remove(self, obs_id: Any, rec: _Record) -> None:
        del self._records[obs_id]
        self._tree.add(rec.value, -rec.weight)
        if rec.subject is not None:
            ids = self._subjects.get(rec.subject)
            if ids is not None:
                ids.discard(obs_id)
                if not ids:
                    del self._subjects[rec.subject]

    def retract(self, obs_id: Any) -> bool:
        """Remove one observation by ID.  Returns True if it existed."""
        rec = self._records.get(obs_id)
        if rec is None:
            return False
        self._remove(obs_id, rec)
        return True

    def retract_subject(self, subject: Any) -> int:
        """Remove every observation belonging to ``subject``."""
        ids = list(self._subjects.get(subject, ()))
        for obs_id in ids:
            self.retract(obs_id)
        return len(ids)

    def advance(self, now: Any) -> List[Any]:
        """Expire observations with ``timestamp <= now - window``.

        Returns the IDs that were removed.  No-op (empty list) when the
        store has no sliding window configured.
        """
        if self._window is None:
            return []
        cutoff = now - self._window
        expired = []
        while self._expiry and self._expiry[0][0] <= cutoff:
            ts, _, obs_id = heapq.heappop(self._expiry)
            rec = self._records.get(obs_id)
            if rec is None or rec.timestamp != ts:
                continue  # stale heap entry: updated or already retracted
            self._remove(obs_id, rec)
            expired.append(obs_id)
        return expired

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def __len__(self) -> int:
        return len(self._records)

    def total_weight(self) -> Fraction:
        return self._tree.total_weight()

    def quantile(self, q: Any) -> QuantileResult:
        """Exact weighted q-quantile as a value interval (label: exact)."""
        lo, hi = self._tree.quantile_interval(Fraction(q))
        return QuantileResult(lo, hi, EXACT)

    def cdf(self, x: Any) -> Fraction:
        """Weighted empirical CDF: fraction of total weight with value <= x."""
        total = self._tree.total_weight()
        if total == 0:
            raise ValueError("cdf of empty store")
        return Fraction(self._tree.weight_le(x), total)

    def items(self) -> Iterator[Tuple[Any, Fraction]]:
        """Yield merged ``(value, weight)`` pairs in ascending order."""
        return self._tree.items()

    # ------------------------------------------------------------------
    # instrumentation
    # ------------------------------------------------------------------
    def node_visits(self) -> int:
        """Treap node accesses since the last ``reset_stats``."""
        return self._tree.visits

    def reset_stats(self) -> None:
        self._tree.reset_stats()

    # ------------------------------------------------------------------
    # shard merge
    # ------------------------------------------------------------------
    def copy(self) -> "WeightedStore":
        clone = WeightedStore(random.Random(self._rng.random()), self._window)
        for obs_id, rec in self._records.items():
            clone.upsert(obs_id, rec.value, rec.weight, rec.subject, rec.timestamp)
        return clone

    def merge(self, other: "WeightedStore") -> "WeightedStore":
        """Merge two shards into a new store.

        Identical duplicate observation IDs merge idempotently; the same
        ID carrying a different payload raises ``ValueError``.
        """
        merged = self.copy()
        for obs_id, rec in other._records.items():
            existing = merged._records.get(obs_id)
            if existing is not None and existing != rec:
                raise ValueError(f"conflicting observation id {obs_id!r}")
            merged.upsert(obs_id, rec.value, rec.weight, rec.subject, rec.timestamp)
        return merged


def shard_index(obs_id: Any, n_shards: int) -> int:
    """Deterministic shard assignment (stable across processes)."""
    return zlib.crc32(repr(obs_id).encode("utf-8")) % n_shards


def split_shards(store: WeightedStore, n_shards: int,
                 seed: int = 0) -> List[WeightedStore]:
    """Split a store into ``n_shards`` shards by observation ID."""
    shards = [WeightedStore(random.Random((seed, i).__hash__()), store._window)
              for i in range(n_shards)]
    for obs_id, rec in store._records.items():
        shard = shards[shard_index(obs_id, n_shards)]
        shard.upsert(obs_id, rec.value, rec.weight, rec.subject, rec.timestamp)
    return shards


def merge_shards(shards: List[WeightedStore]) -> WeightedStore:
    """Merge shards back into a single store."""
    if not shards:
        return WeightedStore()
    merged = shards[0]
    for shard in shards[1:]:
        merged = merged.merge(shard)
    return merged
