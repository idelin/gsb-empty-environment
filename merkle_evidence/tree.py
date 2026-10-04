"""Ordered Merkle tree over evidence records with deterministic padding.

Padding rule (rule:pad-leaf@1 / rule:pad-subtree@1): the leaf level is
extended to the next power of two with deterministic padding leaves
``H_pad(version, index)``.  Because padding is a pure function of the
scheme version and the *true* leaf count, the tree root commits to the
tree size — a prover cannot inflate or shrink the size without changing
the root.  Odd leaf counts are therefore handled uniformly at every
level: after padding, every level is even.
"""

from __future__ import annotations

from .hashing import (leaf_hash, node_hash, empty_root, padding_leaf_hash, root_wrap)


def padded_size(n: int) -> int:
    """Smallest power of two >= max(n, 1)."""
    p = 1
    while p < n:
        p <<= 1
    return p


class MerkleTree:
    """Append-only ordered evidence tree."""

    def __init__(self, version: str):
        self.version = version
        self.records: list[bytes] = []
        self._levels: list[list[bytes]] | None = None

    def append(self, record: bytes) -> int:
        if not isinstance(record, (bytes, bytearray)):
            raise TypeError("record must be bytes")
        self.records.append(bytes(record))
        self._levels = None
        return len(self.records) - 1

    @property
    def size(self) -> int:
        return len(self.records)

    def levels(self) -> list[list[bytes]]:
        """All levels bottom-up; level 0 is the padded leaf level."""
        if self._levels is not None:
            return self._levels
        n = self.size
        if n == 0:
            self._levels = []
            return self._levels
        p = padded_size(n)
        level = [
            leaf_hash(self.version, i, self.records[i]) if i < n
            else padding_leaf_hash(self.version, i)
            for i in range(p)
        ]
        levels = [level]
        while len(level) > 1:
            level = [
                node_hash(self.version, level[i], level[i + 1])
                for i in range(0, len(level), 2)
            ]
            levels.append(level)
        self._levels = levels
        return levels

    def root(self) -> bytes:
        if self.size == 0:
            return empty_root(self.version)
        return root_wrap(self.version, self.size, self.levels()[-1][0])
