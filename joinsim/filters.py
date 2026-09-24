"""Dynamic filters: exact set or Bloom, with injectable hash functions.

A filter is only *consulted* when complete (see build.py). A Bloom filter may
return false positives ("maybe present" for an absent key); that only adds
work -- the probe of the build table decides correctness. False negatives are
impossible because a complete filter has seen every build key of its epoch.
"""
from __future__ import annotations

import hashlib
from typing import Any, Callable, Iterable

HashFn = Callable[[Any], int]


def default_hash_fns(n: int) -> list[HashFn]:
    """n independent hashes derived from sha256 with distinct seeds."""
    def make(seed: int) -> HashFn:
        def h(key: Any) -> int:
            digest = hashlib.sha256(repr((seed, key)).encode()).digest()
            return int.from_bytes(digest[:8], "big")
        return h
    return [make(i) for i in range(n)]


class ExactSetFilter:
    """Zero false positives; used as the ground-truth filter implementation."""

    kind = "exact"

    def __init__(self) -> None:
        self._keys: set = set()

    def add(self, key: Any) -> None:
        self._keys.add(key)

    def might_contain(self, key: Any) -> bool:
        return key in self._keys


class BloomFilter:
    """Classic bit-array Bloom filter. Hash functions are injected so tests
    can force collisions (e.g. ``lambda key: 0``) and verify that false
    positives only ever add work, never drop rows."""

    kind = "bloom"

    def __init__(self, num_bits: int = 64, hash_fns: Iterable[HashFn] | None = None) -> None:
        if num_bits <= 0:
            raise ValueError("num_bits must be positive")
        self.num_bits = num_bits
        self.hash_fns = list(hash_fns) if hash_fns is not None else default_hash_fns(3)
        if not self.hash_fns:
            raise ValueError("at least one hash function required")
        self._bits = 0  # arbitrary-precision int used as a bitset

    def add(self, key: Any) -> None:
        for h in self.hash_fns:
            self._bits |= 1 << (h(key) % self.num_bits)

    def might_contain(self, key: Any) -> bool:
        return all(self._bits >> (h(key) % self.num_bits) & 1 for h in self.hash_fns)
