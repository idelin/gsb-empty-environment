"""Versioned partition function."""
from __future__ import annotations

import hashlib
from dataclasses import dataclass


@dataclass(frozen=True)
class PartitionFunction:
    version: int
    num_partitions: int

    def partition_for(self, table: str, pk: str) -> int:
        digest = hashlib.sha256(f"{table}:{pk}".encode("utf-8")).digest()
        return int.from_bytes(digest[:8], "big") % self.num_partitions
