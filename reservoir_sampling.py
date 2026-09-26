"""Mergeable reservoir sampling over streams of unknown length.

Algorithm: priority-key sampling (A-Res style). Every stream item receives
an i.i.d. Uniform(0, 1) priority key drawn from a caller-injected random
source; a summary retains the ``k`` items with the *smallest* keys seen so
far. Because the retained set is always exactly the global ``k`` smallest
keys, summaries merge by "union, then keep the ``k`` smallest keys", which
is associative, commutative and idempotent, and produces *exactly* the
same keyed sample as processing the concatenated stream centrally. No
concatenation of raw samples is ever involved, so small shards are not
over-represented: an item's survival depends only on its key, never on
which shard it came from or how large that shard was.

Each retained item carries its priority key and a unique identity
``(shard_id, sequence)``, which is the "merge evidence": merging is pure
key comparison, and retries of the same merge are deduplicated by identity
so nothing is ever counted twice.

Capability boundaries (see README.md for the full discussion):

* Length corrections are supported via :meth:`correct_length`, but they
  only affect the reported total ``n``; the sample itself never depends on
  lengths. Corrections are local and can be overwritten by a later merge
  carrying a larger (stale) length for the same shard, because shard
  lengths merge append-only (max).
* Retractions are supported via :meth:`retract`, but items already evicted
  from the reservoir cannot be restored, so after a retraction the sample
  may hold fewer than ``min(k, n)`` items.
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from typing import Any, Callable, Dict, Hashable, Iterable, List, Optional, Tuple

Identity = Tuple[Hashable, int]


@dataclass(frozen=True)
class KeyedItem:
    """A retained sample element together with its merge evidence."""

    identity: Identity
    key: float
    value: Any


class ReservoirSample:
    """Mergeable k-sample of an append-only stream of unknown length.

    Parameters
    ----------
    k:
        Reservoir capacity. ``k == 0`` is allowed: nothing is stored and no
        random keys are drawn, only stream lengths are tracked.
    shard_id:
        Identity of the stream shard this summary appends to. Must be
        unique among all nodes that ever call :meth:`add`; merged summaries
        that keep appending must use their own fresh ``shard_id``.
    rng:
        Caller-injected random source: a zero-argument callable returning
        floats in ``[0, 1)``. If omitted, ``random.Random(seed)`` is used.
    seed:
        Recorded in :meth:`metadata` for reproducibility; only used when
        ``rng`` is not provided.
    """

    def __init__(
        self,
        k: int,
        shard_id: Hashable,
        rng: Optional[Callable[[], float]] = None,
        seed: Optional[int] = None,
    ) -> None:
        if not isinstance(k, int) or k < 0:
            raise ValueError(f"k must be a non-negative integer, got {k!r}")
        self.k = k
        self.shard_id = shard_id
        self.seed = seed
        if rng is None:
            rng = random.Random(seed).random
        self._rng = rng
        self._items: Dict[Identity, KeyedItem] = {}
        self._shard_lengths: Dict[Hashable, int] = {}
        self._next_seq = 0
        self.rng_draws = 0

    # ------------------------------------------------------------------
    # Ingestion
    # ------------------------------------------------------------------
    def add(self, value: Any) -> "ReservoirSample":
        """Append one stream item."""
        self._shard_lengths[self.shard_id] = (
            self._shard_lengths.get(self.shard_id, 0) + 1
        )
        seq = self._next_seq
        self._next_seq += 1
        if self.k == 0:
            return self  # count length only; store nothing, draw nothing
        key = self._rng()
        if not 0.0 <= key < 1.0:
            raise ValueError(f"rng must return floats in [0, 1), got {key!r}")
        self.rng_draws += 1
        self._insert(KeyedItem((self.shard_id, seq), key, value))
        return self

    def add_all(self, values: Iterable[Any]) -> "ReservoirSample":
        for value in values:
            self.add(value)
        return self

    def _insert(self, item: KeyedItem) -> None:
        if item.identity in self._items:
            return  # duplicate delivery / merge retry: never double-count
        if len(self._items) < self.k:
            self._items[item.identity] = item
            return
        worst = max(self._items.values(), key=lambda ki: ki.key)
        if item.key < worst.key:
            del self._items[worst.identity]
            self._items[item.identity] = item

    # ------------------------------------------------------------------
    # Merging
    # ------------------------------------------------------------------
    def merge(self, other: "ReservoirSample") -> "ReservoirSample":
        """Merge ``other`` into ``self`` (in place). Returns ``self``.

        The merge is associative, commutative and idempotent: merging the
        same summary any number of times (retries, diamond topologies)
        yields the same sample and the same total length as merging once.
        """
        if self.k != other.k:
            raise ValueError(
                f"cannot merge summaries with different k: {self.k} vs {other.k}"
            )
        for sid, length in other._shard_lengths.items():
            if length > self._shard_lengths.get(sid, 0):
                self._shard_lengths[sid] = length
        if self.k:
            for item in other._items.values():
                self._insert(item)
        return self

    @classmethod
    def merged(
        cls,
        summaries: Iterable["ReservoirSample"],
        shard_id: Hashable = "merged",
        rng: Optional[Callable[[], float]] = None,
        seed: Optional[int] = None,
    ) -> "ReservoirSample":
        """Build a fresh summary from ``summaries``; it may keep appending."""
        summaries = list(summaries)
        if not summaries:
            raise ValueError("nothing to merge")
        result = cls(summaries[0].k, shard_id=shard_id, rng=rng, seed=seed)
        for summary in summaries:
            result.merge(summary)
        return result

    # ------------------------------------------------------------------
    # Length corrections and retractions (capability boundaries)
    # ------------------------------------------------------------------
    def correct_length(self, shard_id: Hashable, new_length: int) -> None:
        """Correct a previously reported shard length (e.g. upstream recount).

        Only the reported total ``n`` changes; the sample is unaffected
        because priority keys never depend on lengths. Boundary: the
        correction is local -- a later merge carrying a larger stale length
        for the same shard wins (append-only max-merge), so corrections
        must be applied at the authoritative shard or at the final
        aggregation point.
        """
        if shard_id not in self._shard_lengths:
            raise KeyError(f"unknown shard {shard_id!r}")
        if new_length < 0:
            raise ValueError("length cannot be negative")
        retained = sum(1 for ident in self._items if ident[0] == shard_id)
        if new_length < retained:
            raise ValueError(
                f"cannot set length of shard {shard_id!r} to {new_length}: "
                f"{retained} of its items are retained in the reservoir"
            )
        self._shard_lengths[shard_id] = new_length

    def retract(self, identity: Identity) -> bool:
        """Withdraw one previously added item. Returns True if it was held.

        Boundary: items already evicted from the reservoir cannot be
        restored, so after a retraction the sample may contain fewer than
        ``min(k, n)`` items. The caller must guarantee ``identity`` was
        actually added; if its shard is known the length is decremented
        whether or not the item is still held.
        """
        sid, _ = identity
        if sid not in self._shard_lengths:
            raise KeyError(f"unknown shard {sid!r}")
        if self._shard_lengths[sid] <= 0:
            raise ValueError(f"length of shard {sid!r} is already zero")
        self._shard_lengths[sid] -= 1
        return self._items.pop(identity, None) is not None

    # ------------------------------------------------------------------
    # Accessors
    # ------------------------------------------------------------------
    @property
    def n(self) -> int:
        """Total stream length seen across all merged shards."""
        return sum(self._shard_lengths.values())

    @property
    def sample_size(self) -> int:
        return len(self._items)

    def keyed_sample(self) -> List[KeyedItem]:
        """Retained items with their priority keys, ordered by key."""
        return sorted(self._items.values(), key=lambda ki: ki.key)

    def sample(self) -> List[Any]:
        """The sampled values. If the stream was shorter than k, all of it."""
        return [item.value for item in self.keyed_sample()]

    def metadata(self) -> Dict[str, Any]:
        return {
            "k": self.k,
            "n": self.n,
            "sample_size": self.sample_size,
            "seed": self.seed,
            "rng_draws": self.rng_draws,
            "shard_lengths": dict(self._shard_lengths),
        }
