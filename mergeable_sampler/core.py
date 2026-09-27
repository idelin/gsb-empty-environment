"""Mergeable equal-probability k-sampling for streams of unknown length.

Algorithm: bottom-k sampling (priority-key sampling).

* Every offered item receives a uniform priority key in [0, 1) from a
  caller-injected random source.
* A shard retains the k items with the smallest keys seen so far, plus its
  stream length ``n``.
* Merging keeps the k smallest keys of the union of retained entries.

The global k smallest keys are always contained in the union of the
per-shard k smallest keys, so a merged sample is *exactly* the bottom-k
sample of the concatenated stream.  Every k-subset of the stream is
equally likely, regardless of shard sizes or the shape of the merge tree.
The priority keys carried by each retained item are the verifiable
evidence that makes this exact merge possible.

A naive "concatenate the shard samples" merge is biased towards small
shards (each shard contributes up to k entries no matter how short it
is); bottom-k merging has no such bias.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass
from typing import Any, Dict, Hashable, Iterable, List, Tuple

__all__ = ["Entry", "ShardSnapshot", "Shard", "Merger"]


@dataclass(frozen=True)
class Entry:
    """A retained item with its verifiable priority key and unique identity.

    ``(origin, seq)`` identifies the item, so re-merging the same shard
    state (e.g. a retried upload) can never count an item twice.
    """

    origin: Hashable
    seq: int
    key: float
    item: Any

    @property
    def identity(self) -> Tuple[Hashable, int]:
        return (self.origin, self.seq)


@dataclass(frozen=True)
class ShardSnapshot:
    """Immutable, self-consistent state of one shard (or merged subtree).

    Validation rejects inconsistent states, e.g. a length "correction"
    that is not matched by the retained entries.
    """

    shard_id: Hashable
    k: int
    n: int
    entries: Tuple[Entry, ...]

    def __post_init__(self) -> None:
        if self.k < 0:
            raise ValueError("k must be >= 0")
        if self.n < 0:
            raise ValueError("n must be >= 0")
        expected = min(self.k, self.n)
        if len(self.entries) != expected:
            raise ValueError(
                f"inconsistent snapshot: {len(self.entries)} entries, "
                f"expected min(k={self.k}, n={self.n})={expected}"
            )
        seen = set()
        for e in self.entries:
            if e.origin != self.shard_id:
                raise ValueError(
                    f"entry origin {e.origin!r} != shard {self.shard_id!r}"
                )
            if not 0 <= e.seq < self.n:
                raise ValueError(f"entry seq {e.seq} outside [0, n={self.n})")
            if not 0.0 <= e.key < 1.0:
                raise ValueError(f"key {e.key} outside [0, 1)")
            if e.identity in seen:
                raise ValueError(f"duplicate entry identity {e.identity}")
            seen.add(e.identity)


class Shard:
    """Append-only sampler for one stream shard.

    The shard may be snapshotted at any time and may keep accepting items
    afterwards; the newer snapshot simply replaces the older one at merge
    time.  ``rng`` is injected by the caller (anything with ``.random()``,
    e.g. ``RecordedRandom``).
    """

    def __init__(self, shard_id: Hashable, k: int, rng) -> None:
        if k < 0:
            raise ValueError("k must be >= 0")
        self.shard_id = shard_id
        self.k = k
        self.rng = rng
        self.n = 0
        self._heap: List[Tuple[float, int, Entry]] = []  # max-heap via -key

    def offer(self, item: Any) -> None:
        key = self.rng.random()
        if not 0.0 <= key < 1.0:
            raise ValueError("rng.random() must return a value in [0, 1)")
        entry = Entry(self.shard_id, self.n, key, item)
        self.n += 1
        if self.k == 0:
            return  # k = 0: count only, retain nothing
        if len(self._heap) < self.k:
            heapq.heappush(self._heap, (-key, entry.seq, entry))
        elif key < -self._heap[0][0]:
            heapq.heapreplace(self._heap, (-key, entry.seq, entry))

    def offer_many(self, items: Iterable[Any]) -> None:
        for item in items:
            self.offer(item)

    def entries(self) -> Tuple[Entry, ...]:
        return tuple(
            sorted((e for _, _, e in self._heap), key=lambda e: (e.key, e.seq))
        )

    def sample(self) -> List[Any]:
        return [e.item for e in self.entries()]

    def snapshot(self) -> ShardSnapshot:
        return ShardSnapshot(self.shard_id, self.k, self.n, self.entries())


class Merger:
    """Merges shard snapshots (or snapshots of other mergers) by replacement.

    * Idempotent: merging the same snapshot twice changes nothing, so
      retried uploads never double count items or lengths.
    * Append-friendly: a shard that grows simply sends a newer snapshot;
      its previous contribution is replaced, never added again.
    * Hierarchical: ``snapshot()`` turns this merger into a snapshot that
      a parent merger can merge, with the same guarantees.

    Capability boundary for corrections/retractions: a merger accepts a
    corrected length or retracted items only as a complete, self-consistent
    replacement snapshot produced by the origin (which must rebuild its
    own state).  A merger cannot remove individual items by itself:
    bottom-k eviction is irreversible and evicted entries are gone.
    """

    def __init__(self, k: int) -> None:
        if k < 0:
            raise ValueError("k must be >= 0")
        self.k = k
        self._states: Dict[Hashable, ShardSnapshot] = {}

    def merge(self, snap: ShardSnapshot) -> None:
        if snap.k != self.k:
            raise ValueError(
                f"k mismatch: merger k={self.k}, snapshot k={snap.k}"
            )
        self._states[snap.shard_id] = snap

    @property
    def n(self) -> int:
        return sum(s.n for s in self._states.values())

    @property
    def shard_ids(self) -> Tuple[Hashable, ...]:
        return tuple(self._states)

    def entries(self) -> Tuple[Entry, ...]:
        pool = [e for s in self._states.values() for e in s.entries]
        pool.sort(key=lambda e: (e.key, repr(e.origin), e.seq))
        return tuple(pool[: self.k])

    def sample(self) -> List[Any]:
        return [e.item for e in self.entries()]

    def snapshot(self, snapshot_id: Hashable) -> ShardSnapshot:
        """Snapshot this (sub)tree so a parent merger can merge it.

        Entries are re-tagged to ``snapshot_id``; replace semantics at the
        parent level then apply to this whole subtree.
        """
        entries = tuple(
            Entry(snapshot_id, i, e.key, e.item)
            for i, e in enumerate(self.entries())
        )
        return ShardSnapshot(snapshot_id, self.k, self.n, entries)
