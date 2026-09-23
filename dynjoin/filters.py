"""Dynamic filters: exact set or Bloom filter, with an injectable hash.

hash_fn(key, seed) -> int is supplied by the caller, so tests can inject a
constant/colliding hash and force worst-case Bloom false positives. False
positives may only add join work, never change results.
"""
from __future__ import annotations

import hashlib


def default_hash(key, seed):
    digest = hashlib.sha256(repr((key, seed)).encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big")


class ExactFilter:
    """Zero false positives: the authoritative set of normalized keys."""

    kind = "exact"

    def __init__(self):
        self.keys = set()

    def add(self, nkey):
        self.keys.add(nkey)

    def might_contain(self, nkey):
        return nkey in self.keys


class BloomFilter:
    """Classic Bloom filter over an int bitset."""

    kind = "bloom"

    def __init__(self, size, num_hashes, hash_fn):
        self.size = size
        self.num_hashes = num_hashes
        self.hash_fn = hash_fn
        self.bits = 0

    def _positions(self, nkey):
        for i in range(self.num_hashes):
            yield self.hash_fn(nkey, i) % self.size

    def add(self, nkey):
        for pos in self._positions(nkey):
            self.bits |= 1 << pos

    def might_contain(self, nkey):
        return all((self.bits >> pos) & 1 for pos in self._positions(nkey))
