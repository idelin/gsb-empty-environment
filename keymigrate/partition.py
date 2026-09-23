"""Versioned partition functions mapping entity keys to owners."""
from __future__ import annotations

import hashlib

from .events import Key


class PartitionFunction:
    """Deterministic key -> owner mapping. Bumping `version` (and usually
    `num_partitions`) changes the mapping and triggers migration."""

    def __init__(self, version: int, num_partitions: int):
        if num_partitions < 1:
            raise ValueError("num_partitions must be >= 1")
        self.version = version
        self.num_partitions = num_partitions

    def owner_for(self, key: Key) -> str:
        digest = hashlib.sha256(f"{key[0]}:{key[1]}".encode("utf-8")).digest()
        slot = int.from_bytes(digest[:8], "big") % self.num_partitions
        return f"P{slot}"

    def all_owners(self) -> list[str]:
        return [f"P{i}" for i in range(self.num_partitions)]
